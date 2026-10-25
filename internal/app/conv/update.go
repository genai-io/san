// Handler logic for core.Agent outbox events.
package conv

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/genai-io/sdk-go/pkg/ai"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/tool"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
)

// Update routes all output-path messages: agent outbox, permission gate,
// compaction results, and activity updates.
func Update(rt Runtime, m *Model, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case AgentOutboxMsg:
		// A rebuild stops one agent and starts the next; whatever the old
		// outbox still delivers belongs to an agent that is gone, and applying
		// it (its stop above all) would act on the new one.
		if msg.Outbox != nil && msg.Outbox != rt.AgentOutbox() {
			return nil, true
		}
		if msg.Closed && len(msg.Batch) == 0 {
			m.Stream.Stop()
			return rt.OnAgentStop(nil), true
		}
		if len(msg.Batch) > 0 {
			return handleAgentEventBatch(rt, m, msg.Batch, msg.Closed), true
		}
		return handleAgentEvent(rt, m, msg.Event), true
	case PermGateMsg:
		return rt.HandlePermGate(msg.Request), true
	case CompactResultMsg:
		return rt.HandleCompactResult(msg), true
	case AgentActivityBatchMsg:
		for _, activity := range msg {
			m.applyActivity(activity)
		}
		return m.HandleActivityTick(rt.HasRunningTasks() || m.Stream.Active), true
	case AgentActivityMsg:
		m.applyActivity(msg)
		return m.HandleActivityTick(rt.HasRunningTasks() || m.Stream.Active), true
	case AgentToUITickMsg:
		return m.HandleActivityTick(rt.HasRunningTasks()), true
	default:
		return nil, false
	}
}

// --- Agent event dispatch ---

func handleAgentEvent(rt Runtime, m *Model, ev core.Event) tea.Cmd {
	log.Logger().Sugar().Debugf("handleAgentEvent: %T", ev)
	switch e := ev.(type) {
	case core.TurnEnded:
		m.Stream.Stop()
		m.Tool.ClearPending()
		return rt.OnTurnEnd(e.Result)
	case core.AgentStopped:
		m.Stream.Stop()
		m.Tool.ClearPending()
		return rt.OnAgentStop(e.Err)
	case core.Compacted:
		return rt.OnCompacted(e)
	default:
		// A single event emits at most one scrollback write (extra), and
		// ContinueOutbox never writes scrollback, so there's nothing to order —
		// tea.Batch is safe here (unlike the multi-effect batch handler, which
		// sequences). See handleAgentEventBatch.
		if extra := applyAgentEvent(rt, m, ev); extra != nil {
			return tea.Batch(extra, rt.ContinueOutbox())
		}
		return rt.ContinueOutbox()
	}
}

func handleAgentEventBatch(rt Runtime, m *Model, events []core.Event, closed bool) tea.Cmd {
	var effects []tea.Cmd
	needsContinue := true

	for _, ev := range events {
		log.Logger().Sugar().Debugf("handleAgentEventBatch: %T", ev)
		switch e := ev.(type) {
		case core.TurnEnded:
			m.Stream.Stop()
			m.Tool.ClearPending()
			effects = append(effects, rt.OnTurnEnd(e.Result))
			needsContinue = false
		case core.AgentStopped:
			m.Stream.Stop()
			m.Tool.ClearPending()
			effects = append(effects, rt.OnAgentStop(e.Err))
			needsContinue = false
		case core.Compacted:
			effects = append(effects, rt.OnCompacted(e))
			needsContinue = false
		default:
			if extra := applyAgentEvent(rt, m, ev); extra != nil {
				effects = append(effects, extra)
			}
			continue
		}
		break // terminal event — don't process further events in this batch
	}

	if closed {
		m.Stream.Stop()
		m.Tool.ClearPending()
		effects = append(effects, rt.OnAgentStop(nil))
		needsContinue = false
	}

	// Effects that write scrollback (commit/flush Println) must keep their
	// emission order. tea.Batch runs commands in concurrent goroutines with no
	// ordering guarantee, which can interleave progressively-committed blocks;
	// tea.Sequence runs them in order. The outbox drain stays concurrent — it
	// only reads the next event and never touches scrollback.
	var ordered tea.Cmd
	switch {
	case len(effects) == 1:
		ordered = effects[0]
	case len(effects) > 1:
		ordered = tea.Sequence(effects...)
	}
	if !needsContinue {
		return ordered
	}
	if ordered == nil {
		return rt.ContinueOutbox()
	}
	return tea.Batch(ordered, rt.ContinueOutbox())
}

// --- Event side-effect handlers (no ContinueOutbox) ---

func applyAgentEvent(rt Runtime, m *Model, ev core.Event) tea.Cmd {
	switch e := ev.(type) {
	case sdkagent.MessageAdded:
		// PostTool filters run after ToolEnd. Only the native result added to
		// the conversation carries an attached recall; show that use here,
		// not at turn end, without replacing the raw output already drawn.
		for _, result := range e.Message.ToolResults() {
			if notice := recalledMemoryNotice(result.Text(), e.Message.ID); notice != "" {
				m.AddNotice(notice)
			}
		}
		return nil
	case sdkagent.CompactionStart:
		return rt.OnCompactStart(len(e.Messages))
	case sdkagent.CompactionEnd:
		return rt.OnCompactEnd(e)
	case sdkagent.InferenceStart:
		// A retry announces itself by the attempt number, not by an event of
		// its own: what was drawn for the previous attempt is void, and the
		// next one re-streams from scratch.
		if e.Attempt > 1 {
			m.DropStreamingAssistant()
			m.Stream.Stop()
		}
		cmd := applyPreInfer(rt, m)
		m.Stream.Attempt = max(1, e.Attempt)
		return cmd
	case sdkagent.InferenceUpdate:
		return applyChunk(rt, m, e.Delta)
	case sdkagent.InferenceEnd:
		m.Stream.EndInference()
		if e.Err != nil {
			rt.OnInference(e.Response)
			if !errors.Is(e.Err, context.Canceled) {
				m.AddNotice(fmt.Sprintf("Inference attempt %d failed: %v", e.Attempt, e.Err))
			}
			return tea.Batch(rt.CommitMessages()...)
		}
		return applyPostInfer(rt, m, e.Response)
	case sdkagent.ToolUpdate:
		applyToolRunning(m, e.ID, e.Name)
		return nil
	case sdkagent.ToolEnd:
		return applyPostTool(rt, m, core.ToolResult{
			ToolCallID: e.ID,
			ToolName:   e.Name,
			Content:    sdkagent.ResultContent(e.Result, e.Err),
			IsError:    e.Err != nil,
		}, e.Result.Details)
	default:
		// AgentStarted, MessageReceived, and the loop's events the interface
		// has nothing to draw for. A message entering the inbox is already on
		// screen: every path that hands one to the agent appends it at the call
		// site, so acting on the echo would draw it twice.
		return nil
	}
}

func applyPreInfer(rt Runtime, m *Model) tea.Cmd {
	// A new inference means any auto-compaction is over: a successful one already
	// cleared this in OnCompacted, but a failed compaction falls straight through
	// to here, so clear the in-progress indicator either way.
	m.Compact.Clear()
	// The submission's wait carries into its first inference; each later step
	// is a new wait and times from zero.
	started := m.Stream.StartedAt
	if !m.Stream.Waiting {
		started = time.Now()
	}
	m.Stream.Stop()
	commitCmds := rt.CommitMessages()
	msg := m.Append(core.ChatMessage{Role: core.ChatAssistant})
	m.Stream = StreamState{Active: true, Inferring: true, MessageID: msg.ID, StartedAt: started}
	cmds := append(commitCmds, m.Spinner.Tick)
	return tea.Batch(cmds...)
}

func applyChunk(rt Runtime, m *Model, chunk ai.Event) tea.Cmd {
	// Late chunks after handleStreamCancel has flipped Stream off and
	// appended the [Interrupted] marker would otherwise call AppendStream
	// and bleed text past the marker. RenderAssistantMessage's suffix
	// strip then fails and a literal "[Interrupted]" renders inline.
	if !m.Stream.Active {
		return nil
	}
	if chunk.Type == ai.EventProgress {
		// Progress is display-only. It must never install a pending tool call
		// or append parameter fragments to the assistant's text.
		if m.Stream.Inferring && chunk.Progress != nil {
			m.Stream.GeneratingTool = chunk.Progress.ToolName
			m.Stream.GeneratedBytes = chunk.Progress.InputBytes
		}
		return nil
	}
	// The wire says which block a fragment belongs to, where San's own chunk
	// had two named fields and could carry nothing else.
	if chunk.Type == ai.EventBlockDelta && chunk.Block.Text != "" {
		m.Stream.GeneratingTool = ""
		m.Stream.GeneratedBytes = 0
		switch chunk.Block.Type {
		case ai.BlockText:
			m.AppendStream(chunk.Block.Text, "")
		case ai.BlockThinking:
			m.AppendStream("", chunk.Block.Text)
		}
	}
	// Final chunk of a text-only turn: commit the streaming message's remaining
	// tail (its completed blocks are already in scrollback) in a single Println.
	if chunk.Type == ai.EventDone && chunk.Response != nil && len(chunk.Response.ToolCalls()) == 0 {
		// InferenceEnd still needs the identity to attach final metadata.
		m.Stream.Active = false
		m.Stream.ThinkingActive = false
		if commitCmds := rt.CommitMessages(); len(commitCmds) > 0 {
			return tea.Batch(commitCmds...)
		}
		return nil
	}
	// Mid-stream: flush any blocks that just completed (thinking once text
	// starts, content at each fence-close / blank-line boundary) to scrollback.
	if flushCmds := rt.FlushStreamingBlocks(); len(flushCmds) > 0 {
		return tea.Batch(flushCmds...)
	}
	return nil
}

func applyPostInfer(rt Runtime, m *Model, resp *ai.Response) tea.Cmd {
	if resp == nil {
		return nil
	}
	rt.OnInference(resp)
	m.Compact.WarningSuppressed = false
	// Done closes text streaming before InferenceEnd attaches metadata; cancellation
	// clears the identity instead, so late metadata cannot revive a cancelled call.
	if m.StreamMessageIndex() < 0 {
		return nil
	}
	if sig := resp.Content.ThinkingSignature(); sig != "" {
		m.SetStreamThinkingSignature(sig)
	}
	if calls := resp.Content.ToolCalls(); len(calls) > 0 {
		m.SetStreamToolCalls(calls)
		m.Tool.Track(calls)
	}
	m.Stream.MessageID = ""
	m.Stream.BuildingTool = ""
	return nil
}

// applyToolRunning marks the call whose run has begun (core's withCallID
// reports it), not the one ToolStart announced: a batch is announced whole and
// then run a call at a time, so a queued call has no timer until it runs.
func applyToolRunning(m *Model, id, name string) {
	m.Stream.BuildingTool = name
	m.Tool.MarkCurrent(id)
	m.Tool.MarkStarted(id)
}

func applyPostTool(rt Runtime, m *Model, tr core.ToolResult, details any) tea.Cmd {
	m.Stream.BuildingTool = ""
	if tool.IsAgentToolName(tr.ToolName) {
		delete(m.TaskActivity, m.Tool.IndexOf(tr.ToolCallID))
	}
	batchComplete := m.Tool.MarkComplete(tr.ToolCallID)
	// A tool that completed just before the user pressed Esc may have its
	// PostToolEvent still buffered in the outbox when handleStreamCancel
	// runs — cancelPendingToolCalls then writes a cancelled-result row for
	// the same ToolCallID. When the buffered event finally drains we'd
	// double-append. Skip if conv already carries a result for this call.
	if tr.ToolCallID != "" {
		for i := range m.Messages {
			if existing := m.Messages[i].ToolResult; existing != nil && existing.ToolCallID == tr.ToolCallID {
				return nil
			}
		}
	}
	m.Append(core.ChatMessage{
		Role:        core.ChatUser,
		ToolResult:  rt.OnToolResult(tr),
		ToolDetails: details,
		// Stamp the auto-review decision (if this call was judged) onto the
		// result message so it renders inline under the tool call. Consumed
		// here — the handoff map keeps only in-flight calls.
		Decision: rt.TakeReviewDecision(tr.ToolCallID),
	})
	// Release queued input only after every tool call in this response has
	// completed. Releasing after the first result inserts the user's pending
	// message between sibling tool rows; waiting preserves the visible order:
	// complete tool output first, then the pending message.
	if batchComplete {
		return rt.OnStepEnd()
	}
	return nil
}

// --- Activity handling (operates on output Model directly) ---

func (m *Model) applyActivity(msg AgentActivityMsg) {
	if msg.ToolCallID != "" {
		msg.Index = m.Tool.IndexOf(msg.ToolCallID)
	}
	if msg.Index < 0 || msg.Index >= len(m.Tool.PendingCalls) {
		return // Late activity from a retired batch must not land on its successor.
	}
	if !tool.IsAgentToolName(m.Tool.PendingCalls[msg.Index].Name) {
		m.Tool.SetProgress(msg.ToolCallID, msg.Message)
		return
	}
	m.retainActivity(msg)
}

func (m *OutputModel) retainActivity(msg AgentActivityMsg) {
	if m.TaskActivity == nil {
		m.TaskActivity = make(map[int][]string)
	}
	m.TaskActivity[msg.Index] = retainAgentActivity(m.TaskActivity[msg.Index], msg.Message)
}

func (m *OutputModel) HandleActivity(msg AgentActivityMsg) tea.Cmd {
	m.retainActivity(msg)

	return m.Spinner.Tick
}

func (m *OutputModel) HandleActivityTick(hasRunningTasks bool) tea.Cmd {
	if hasRunningTasks {
		return m.Spinner.Tick
	}
	return nil
}
