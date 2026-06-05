// Handler logic for core.Agent outbox events.
package conv

import (
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
	if chunk.Done && chunk.Response != nil && len(chunk.Response.ToolCalls) == 0 {
		m.Stream.Active = false
		commitCmds := rt.CommitMessages()
		if len(commitCmds) > 0 {
			return tea.Batch(commitCmds...)
		}
	}
	return nil
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
		ToolName:   tr.ToolName,
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
