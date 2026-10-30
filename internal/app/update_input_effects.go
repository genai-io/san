// Input-driven side effects that don't belong to a single key handler:
// streaming cancel (Ctrl+C / Esc mid-stream), in-flight tool-call
// cancellation, clipboard image paste, and the quit-with-cancel path that
// gracefully stops the agent before tea.Quit.
package app

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/image"
)

type turnFinalizedMsg struct {
	receipt *core.TurnFinalization
	outbox  <-chan core.Event
	err     error
}

func (m *model) handleStreamCancel() tea.Cmd {
	m.backgroundNoticesHeld = true
	m.queueContinuation = nil
	// The truncated reply and the cancelled tool results already tell the
	// model the turn was cut short; no extra reminder is needed.
	receipt := m.services.Agent.CancelTurn()
	outbox := m.services.Agent.Outbox()
	m.pruneInteractions()
	m.cancelPendingToolCalls()
	m.conv.MarkStreamInterrupted()
	m.conv.Stream.Stop()

	cmds := m.CommitMessages()
	if receipt != nil && m.cancellingTurn != receipt {
		m.cancellingTurn = receipt
		cmds = append(cmds, func() tea.Msg {
			// Wait is bounded by the captured run's lifetime, not a UI deadline.
			return turnFinalizedMsg{receipt: receipt, outbox: outbox, err: receipt.Wait(context.Background())}
		})
	}
	return tea.Batch(cmds...)
}

func (m *model) cancelPendingToolCalls() {
	toolCalls := m.conv.Tool.DrainPendingCalls()
	if idx := m.conv.StreamMessageIndex(); toolCalls == nil && idx >= 0 {
		toolCalls = m.conv.Messages[idx].ToolCalls
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
	m.FireSessionEnd("prompt_input_exit")
	// Index flush happens once at the post-Run teardown seam (see run.go), the
	// single point every quit path converges on — including /exit and /quit,
	// which run this same shutdown sequence but never reach here.
	return tea.Quit, true
}
