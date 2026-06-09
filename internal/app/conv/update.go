// Handler logic for core.Agent outbox events.
package conv

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/tool"
)

// Update routes all output-path messages: agent outbox, permission bridge,
// compaction results, and progress updates.
func Update(rt Runtime, m *Model, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case AgentOutboxMsg:
		if msg.Closed && len(msg.Batch) == 0 {
			m.Stream.Stop()
			return rt.OnAgentStop(nil), true
		}
		if len(msg.Batch) > 0 {
			return handleAgentEventBatch(rt, m, msg.Batch, msg.Closed), true
		}
		return handleAgentEvent(rt, m, msg.Event), true
	case PermBridgeMsg:
		return rt.OnPermBridgeRequest(msg.Request), true
	case CompactResultMsg:
		return rt.OnCompactResult(msg), true
	case kit.TokenLimitResultMsg:
		return rt.OnTokenLimitResult(msg), true
	case ProgressUpdateMsg:
		if msg.Index < 0 && msg.ToolCallID != "" {
			msg.Index = m.Tool.IndexOf(msg.ToolCallID)
		}
		if msg.Index < 0 {
			return m.HandleProgressTick(rt.HasRunningTasks()), true
		}
		return m.HandleProgress(msg), true
	case ProgressCheckTickMsg:
		return m.HandleProgressTick(rt.HasRunningTasks()), true
	default:
		return nil, false
	}
}

// --- Agent event dispatch ---

func handleAgentEvent(rt Runtime, m *Model, ev core.Event) tea.Cmd {
	log.QueueLog("handleAgentEvent: %s", ev.Type)
	switch ev.Type {
	case core.OnTurn:
		result, _ := ev.Result()
		m.Stream.Stop()
		m.Tool.ClearPending()
		return rt.OnTurnEnd(result)
	case core.OnStop:
		err, _ := ev.Error()
		m.Stream.Stop()
		m.Tool.ClearPending()
		return rt.OnAgentStop(err)
	case core.OnCompact:
		info, _ := ev.CompactInfo()
		return rt.OnCompacted(info)
	default:
		if extra := applyAgentEvent(rt, m, ev); extra != nil {
			return tea.Batch(extra, rt.ContinueOutbox())
		}
		return rt.ContinueOutbox()
	}
}

func handleAgentEventBatch(rt Runtime, m *Model, events []core.Event, closed bool) tea.Cmd {
	var cmds []tea.Cmd
	needsContinue := true

	for _, ev := range events {
		log.QueueLog("handleAgentEventBatch: %s", ev.Type)
		switch ev.Type {
		case core.OnTurn:
			result, _ := ev.Result()
			m.Stream.Stop()
			m.Tool.ClearPending()
			cmds = append(cmds, rt.OnTurnEnd(result))
			needsContinue = false
		case core.OnStop:
			err, _ := ev.Error()
			m.Stream.Stop()
			m.Tool.ClearPending()
			cmds = append(cmds, rt.OnAgentStop(err))
			needsContinue = false
		case core.OnCompact:
			info, _ := ev.CompactInfo()
			cmds = append(cmds, rt.OnCompacted(info))
			needsContinue = false
		default:
			if extra := applyAgentEvent(rt, m, ev); extra != nil {
				cmds = append(cmds, extra)
			}
			continue
		}
		break // terminal event — don't process further events in this batch
	}

	if closed {
		m.Stream.Stop()
		m.Tool.ClearPending()
		cmds = append(cmds, rt.OnAgentStop(nil))
		needsContinue = false
	}

	if needsContinue {
		cmds = append(cmds, rt.ContinueOutbox())
	}

	if len(cmds) == 1 {
		return cmds[0]
	}
	return tea.Batch(cmds...)
}

// --- Event side-effect handlers (no ContinueOutbox) ---

func applyAgentEvent(rt Runtime, m *Model, ev core.Event) tea.Cmd {
	switch ev.Type {
	case core.OnStart:
		return nil
	case core.OnMessage:
		msg, ok := ev.Message()
		if !ok {
			return nil
		}
		return rt.OnAgentMessage(msg)
	case core.PreInfer:
		return applyPreInfer(rt, m)
	case core.OnChunk:
		return applyChunk(rt, m, ev)
	case core.PostInfer:
		return applyPostInfer(rt, m, ev)
	case core.PreTool:
		applyPreTool(m, ev)
		return nil
	case core.PostTool:
		return applyPostTool(rt, m, ev)
	default:
		return nil
	}
}

func applyPreInfer(rt Runtime, m *Model) tea.Cmd {
	rt.OnTurnBegin()
	m.Stream.Active = true
	m.Stream.BuildingTool = ""
	m.Stream.ScrollbackLen = 0
	m.Stream.ThinkingCommitted = false
	commitCmds := rt.CommitMessages()
	m.Append(core.ChatMessage{Role: core.RoleAssistant, Content: ""})
	cmds := append(commitCmds, m.Spinner.Tick)
	return tea.Batch(cmds...)
}

func applyChunk(rt Runtime, m *Model, ev core.Event) tea.Cmd {
	chunk, ok := ev.Chunk()
	if !ok {
		return nil
	}
	// Late chunks after handleStreamCancel has flipped Stream off and
	// appended the [Interrupted] marker would otherwise call AppendToLast
	// and bleed text past the marker. RenderAssistantMessage's suffix
	// strip then fails and a literal "[Interrupted]" renders inline.
	if !m.Stream.Active {
		return nil
	}
	if chunk.Text != "" || chunk.Thinking != "" {
		m.AppendToLast(chunk.Text, chunk.Thinking)
	}

	// Commit newly streamed text to terminal scrollback at newline
	// boundaries so the user can scroll up to see earlier parts of a long
	// response that have been truncated from the active view. Both thinking
	// and content text are committed as plain text — the View() area shows
	// the live formatted tail. When the stream finishes we advance
	// CommittedCount so CommitMessages does not re-print the same message
	// (which would duplicate output).
	var cmds []tea.Cmd
	if cmd := commitStreamTail(m, false); cmd != nil {
		cmds = append(cmds, cmd)
	}

	if chunk.Done && chunk.Response != nil && len(chunk.Response.ToolCalls) == 0 {
		m.Stream.Active = false
		// Flush any remaining tail that hasn't hit a newline yet.
		if cmd := commitStreamTail(m, true); cmd != nil {
			cmds = append(cmds, cmd)
		}
		// commitStreamTail already flushed the full text to scrollback.
		// Skip CommitMessages for this message to avoid duplicating it.
		m.CommittedCount = len(m.Messages)
		commitCmds := rt.CommitMessages()
		if len(commitCmds) > 0 {
			cmds = append(cmds, commitCmds...)
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// commitStreamTail flushes newly streamed assistant text (thinking and
// content) to terminal scrollback and advances Stream.ScrollbackLen past
// whatever it commits.  Thinking text is committed on the first call
// (ScrollbackLen == 0).  When flush is false it commits content only
// through the last newline (so a partial trailing line doesn't appear as
// left-edge flicker); when true it commits the entire remaining tail.
// Returns nil when there is nothing to commit.
func commitStreamTail(m *Model, flush bool) tea.Cmd {
	if len(m.Messages) == 0 {
		return nil
	}
	last := m.Messages[len(m.Messages)-1]
	if last.Role != core.RoleAssistant {
		return nil
	}

	var cmds []tea.Cmd

	// Commit thinking text once — it arrives incrementally but we
	// only want the final version in scrollback. Commit it when
	// content first appears (meaning thinking is done) and only once.
	if len(last.Thinking) > 0 && !m.Stream.ThinkingCommitted && len(last.Content) > 0 {
		cmds = append(cmds, tea.Println(last.Thinking))
		m.Stream.ThinkingCommitted = true
	}

	if len(last.Content) <= m.Stream.ScrollbackLen {
		return pickCmd(cmds)
	}

	delta := last.Content[m.Stream.ScrollbackLen:]
	if flush {
		m.Stream.ScrollbackLen = len(last.Content)
		// Flush thinking if it hasn't been committed yet (edge case:
		// content never triggered the thinking commit above).
		if len(last.Thinking) > 0 && !m.Stream.ThinkingCommitted {
			cmds = append(cmds, tea.Println(last.Thinking))
			m.Stream.ThinkingCommitted = true
		}
		// Render through markdown so headings, bold, tables, etc.
		// appear styled in scrollback instead of raw syntax.
		cmds = append(cmds, tea.Println(renderMarkdownContent(m.MDRenderer, delta)))
		return pickCmd(cmds)
	}

	lastNewline := strings.LastIndex(delta, "\n")
	if lastNewline < 0 {
		return pickCmd(cmds)
	}
	commit := delta[:lastNewline+1]
	m.Stream.ScrollbackLen += len(commit)
	rendered := renderMarkdownContent(m.MDRenderer, strings.TrimRight(commit, "\n"))
	cmds = append(cmds, tea.Println(rendered))
	return pickCmd(cmds)
}

// pickCmd returns the single command, a batch, or nil.
func pickCmd(cmds []tea.Cmd) tea.Cmd {
	switch len(cmds) {
	case 0:
		return nil
	case 1:
		return cmds[0]
	default:
		return tea.Batch(cmds...)
	}
}

func applyPostInfer(rt Runtime, m *Model, ev core.Event) tea.Cmd {
	resp, ok := ev.Response()
	if !ok {
		return nil
	}
	rt.OnTokenUsage(resp)
	m.Compact.WarningSuppressed = false
	// No Stream.Active guard: SetLastThinkingSignature / SetLastToolCalls
	// already bail on non-assistant tails, which is the only way a late
	// PostInfer could corrupt conv state (after cancelPendingToolCalls
	// appended user-role rows). A guard on Stream.Active would also
	// suppress these setters for normal text-only completions, since
	// applyChunk flips Stream.Active=false on the Done chunk that arrives
	// just before this PostInfer — silently dropping ThinkingSignature.
	if resp.ThinkingSignature != "" {
		m.SetLastThinkingSignature(resp.ThinkingSignature)
	}
	if len(resp.ToolCalls) > 0 {
		m.SetLastToolCalls(resp.ToolCalls)
		m.Tool.Track(resp.ToolCalls)
	}
	m.Stream.BuildingTool = ""
	return nil
}

func applyPreTool(m *Model, ev core.Event) {
	if tc, ok := ev.ToolCall(); ok {
		m.Stream.BuildingTool = tc.Name
		m.Tool.MarkCurrent(tc.ID)
	}
}

func applyPostTool(rt Runtime, m *Model, ev core.Event) tea.Cmd {
	tr, ok := ev.ToolResult()
	if !ok {
		return nil
	}
	m.Stream.BuildingTool = ""
	if tool.IsAgentToolName(tr.ToolName) {
		m.TaskProgress = nil
	}
	m.Tool.MarkComplete(tr.ToolCallID)
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
	result := rt.OnToolResult(tr)
	m.Append(core.ChatMessage{
		Role:       core.RoleUser,
		ToolResult: result,
	})
	return nil
}

// --- Progress handling (operates on output Model directly) ---

func (m *OutputModel) drainProgress() {
	if m.ProgressHub == nil {
		return
	}
	m.TaskProgress = m.ProgressHub.Drain(m.TaskProgress)
}

func (m *OutputModel) HandleProgress(msg ProgressUpdateMsg) tea.Cmd {
	if m.TaskProgress == nil {
		m.TaskProgress = make(map[int][]string)
	}
	m.TaskProgress[msg.Index] = append(m.TaskProgress[msg.Index], msg.Message)
	// Cap progress entries per agent to prevent unbounded growth
	if len(m.TaskProgress[msg.Index]) > maxAgentProgressHistory {
		m.TaskProgress[msg.Index] = m.TaskProgress[msg.Index][len(m.TaskProgress[msg.Index])-maxAgentProgressHistory:]
	}

	if m.ProgressHub == nil {
		return m.Spinner.Tick
	}
	return tea.Batch(m.Spinner.Tick, m.ProgressHub.Check())
}

func (m *OutputModel) HandleProgressTick(hasRunningTasks bool) tea.Cmd {
	if m.ProgressHub != nil {
		if hasRunningTasks {
			return tea.Batch(m.Spinner.Tick, m.ProgressHub.Check())
		}
		return m.ProgressHub.Check()
	}
	if hasRunningTasks {
		return m.Spinner.Tick
	}
	return nil
}
