// Input-driven side effects that don't belong to a single key handler:
// streaming cancel (Ctrl+C / Esc mid-stream), in-flight tool-call
// cancellation, clipboard image paste, and the quit-with-cancel path that
// gracefully stops the agent before tea.Quit.
package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/image"
)

func (m *model) handleStreamCancel() tea.Cmd {
	// The truncated reply and the cancelled tool results already tell the
	// model the turn was cut short; no extra reminder is needed.
	m.services.Agent.InterruptTurn()
	m.conv.Stream.Stop()
	m.conv.AgentToUI.DrainPendingQuestions()
	m.conv.Modal.Question.Hide()
	m.cancelPendingToolCalls()
	m.conv.MarkLastInterrupted()

	cmds := m.CommitMessages()
	if cmd := m.drainInputQueueWhileIdle(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

func (m *model) cancelPendingToolCalls() {
	toolCalls := m.conv.Tool.DrainPendingCalls()
	if toolCalls == nil && len(m.conv.Messages) > 0 {
		lastMsg := m.conv.Messages[len(m.conv.Messages)-1]
		if lastMsg.Role == core.ChatAssistant {
			toolCalls = lastMsg.ToolCalls
		}
	}
	m.conv.AppendCancelledToolResults(toolCalls, func(tc core.ToolCall) string {
		return "Tool execution interrupted because the user sent a new message."
	}, m.TakeReviewDecision)
}

func (m *model) pasteImageFromClipboard() (tea.Cmd, bool) {
	imgData, err := image.ReadClipboard()
	if err != nil {
		m.conv.Append(core.ChatMessage{Role: core.ChatNotice, Content: "Image paste error: " + err.Error()})
		return tea.Batch(m.CommitMessages()...), true
	}
	if imgData == nil {
		return nil, false
	}
	label := m.userInput.AddPendingImage(*imgData)
	m.userInput.Images.Selection = input.ImageSelection{}
	m.userInput.Textarea.InsertString(label)
	return nil, true
}

func (m *model) QuitWithCancel() (tea.Cmd, bool) {
	m.services.Agent.Stop()
	m.conv.Stream.Stop()
	if m.conv.Tool.Cancel != nil {
		m.conv.Tool.Cancel()
	}
	m.FireSessionEnd("prompt_input_exit")
	// Index flush happens once at the post-Run teardown seam (see run.go), the
	// single point every quit path converges on — including /exit and /quit,
	// which run this same shutdown sequence but never reach here.
	return tea.Quit, true
}
