// Operation-mode cycling (Shift+Tab) and the question-modal protocol used
// by AskUserQuestion-style prompts surfaced from tools.
package app

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool"
)

func (m *model) cycleOperationMode() tea.Cmd {
	allowBypass := m.services.Setting.AllowBypass()
	m.env.OperationMode = m.env.OperationMode.NextWithBypass(allowBypass)
	m.env.ApplyModePermissions(m.env.CWD)

	m.services.Hook.SetPermissionMode(m.env.OperationModeName())
	// Landing on AutoPilot opens the mission hands-free (Start) or surfaces the
	// opening proposal (Suggest) — but debounce it: the cycle wraps through
	// AutoPilot, so a gesture that only passes through it must not fire a wasted
	// LLM call. Confirm the user actually rested here (handleAutopilotModeSettled).
	if m.env.OperationMode == setting.ModeAutoPilot {
		return tea.Tick(autopilotSettleDelay, func(time.Time) tea.Msg { return autopilotModeSettledMsg{} })
	}
	return nil
}

// autopilotSettleDelay is how long the mode must rest on AutoPilot before the
// kick/suggest fires — long enough to skip a pass-through cycle, short enough to
// feel immediate when the user deliberately stops here.
const autopilotSettleDelay = 250 * time.Millisecond

// autopilotModeSettledMsg fires autopilotSettleDelay after landing on AutoPilot.
type autopilotModeSettledMsg struct{}

// handleAutopilotModeSettled runs the deferred kick/suggest once the mode has
// rested on AutoPilot. It no-ops if the user has since cycled away or a turn is
// in flight, so a pass-through cycle costs nothing.
func (m *model) handleAutopilotModeSettled() tea.Cmd {
	if !m.autopilotEngaged() {
		return nil
	}
	if cmd := m.autopilotKickCmd(); cmd != nil {
		return cmd
	}
	return m.startPromptSuggestion()
}

func (m *model) updateMode(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case conv.QuestionRequestMsg:
		// Questions arrive via AgentToUI.Check(); re-arm the poll so the next
		// progress update or question keeps flowing while the modal is up.
		cmd := m.handleQuestionRequest(msg)
		if m.conv.AgentToUI != nil {
			cmd = tea.Batch(cmd, m.conv.AgentToUI.Check())
		}
		return cmd, true
	case conv.SecretPromptRequestMsg:
		cmd := m.handleSecretPromptRequest(msg)
		if m.conv.AgentToUI != nil {
			cmd = tea.Batch(cmd, m.conv.AgentToUI.Check())
		}
		return cmd, true
	}
	return nil, false
}

func (m *model) handleQuestionRequest(msg conv.QuestionRequestMsg) tea.Cmd {
	m.conv.Modal.PendingQuestion = msg.Request
	m.conv.Modal.PendingQuestionReply = msg.Reply
	m.conv.Modal.Question.Show(msg.Request, m.env.Width)
	cmds := m.CommitMessages()
	// Question steer (#4): show the modal, then let the copilot try to answer it
	// on the user's behalf (it defers back to the human when unsure).
	if cmd := m.autopilotAnswerQuestionCmd(msg.Request); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

func (m *model) handleQuestionResponse(msg conv.QuestionResponseMsg) tea.Cmd {
	reply := m.conv.Modal.PendingQuestionReply
	m.conv.Modal.PendingQuestionReply = nil
	defer func() { m.conv.Modal.PendingQuestion = nil }()

	if reply == nil {
		return nil
	}

	if msg.Cancelled {
		reply <- &tool.QuestionResponse{
			RequestID: msg.Request.ID,
			Cancelled: true,
		}
		return nil
	}
	reply <- msg.Response
	return nil
}

func (m *model) handleSecretPromptRequest(msg conv.SecretPromptRequestMsg) tea.Cmd {
	m.conv.Modal.PendingSecretReply = msg.Reply
	m.userInput.Secret.Show(msg.Prompt, m.env.Width)
	return nil
}

func (m *model) handleSecretPromptResponse(msg input.SecretPromptResponseMsg) tea.Cmd {
	reply := m.conv.Modal.PendingSecretReply
	m.conv.Modal.PendingSecretReply = nil
	if reply == nil {
		return nil
	}
	reply <- conv.SecretPromptResponse{
		Value:     msg.Value,
		Cancelled: msg.Cancelled,
	}
	return nil
}
