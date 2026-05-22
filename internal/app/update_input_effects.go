// Input-driven side effects that don't belong to a single key handler:
// streaming cancel (Ctrl+C / Esc mid-stream), in-flight tool-call
// cancellation, clipboard image paste, and the quit-with-cancel path that
// gracefully stops the agent before tea.Quit.
package app

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/genai-io/gen-code/internal/app/input"
	"github.com/genai-io/gen-code/internal/core"
	"github.com/genai-io/gen-code/internal/image"
)

func (m *model) handleStreamCancel() tea.Cmd {
	m.services.Agent.Stop()
	m.conv.Stream.Stop()
	m.conv.ProgressHub.DrainPendingQuestions()
	m.conv.Modal.Question.Hide()
	m.cancelPendingToolCalls()
	m.conv.MarkLastInterrupted()
	m.conv.AppendInterruptedByUserMarker()

	cmds := m.CommitMessages()
	if cmd := m.drainInputQueueAfterCancel(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

func (m *model) cancelPendingToolCalls() {
	toolCalls := m.conv.Tool.DrainPendingCalls()
	if toolCalls == nil && len(m.conv.Messages) > 0 {
		lastMsg := m.conv.Messages[len(m.conv.Messages)-1]
		if lastMsg.Role == core.RoleAssistant {
			toolCalls = lastMsg.ToolCalls
		}
	}
	m.conv.AppendCancelledToolResults(toolCalls, func(tc core.ToolCall) string {
		if tc.Name == "TaskOutput" {
			return "Stopped waiting for background task output because the user sent a new message. The background task may still be running."
		}
		return "Tool execution interrupted because the user sent a new message."
	})
}

func (m *model) cancelRemainingToolCalls(startIdx int) {
	m.conv.AppendCancelledToolResults(m.conv.Tool.RemainingCalls(startIdx), func(core.ToolCall) string {
		return "Tool execution skipped."
	})
}

func (m *model) pasteImageFromClipboard() (tea.Cmd, bool) {
	imgData, err := image.ReadImageToProviderData()
	if err != nil {
		m.conv.Append(core.ChatMessage{Role: core.RoleNotice, Content: "Image paste error: " + err.Error()})
		return tea.Batch(m.CommitMessages()...), true
	}
	if imgData == nil {
		return nil, false
	}
	label := m.userInput.AddPendingImage(*imgData)
	m.userInput.Images.Selection = input.ImageSelection{}
	m.userInput.Textarea.InsertString(label)
	m.userInput.UpdateHeight()
	return nil, true
}

func (m *model) QuitWithCancel() (tea.Cmd, bool) {
	m.services.Agent.Stop()
	m.conv.Stream.Stop()
	if m.conv.Tool.Cancel != nil {
		m.conv.Tool.Cancel()
	}
	m.FireSessionEnd("prompt_input_exit")
	return tea.Quit, true
}
