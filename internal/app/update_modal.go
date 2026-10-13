// Operation-mode cycling (Shift+Tab) and the question-modal protocol used
// by AskUserQuestion-style prompts surfaced from tools.
package app

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"go.uber.org/zap"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool"
)

func (m *model) cycleOperationMode() tea.Cmd {
	allowBypass := m.services.Setting.AllowBypass()
	m.env.OperationMode = m.env.OperationMode.NextWithBypass(allowBypass)
	m.modeHintUntil = time.Now().Add(modeHintFor)
	m.applyOperationMode()
	m.persistOperationMode()
	// Landing on AutoPilot surfaces the opening proposal (Suggest) — but debounce
	// it: the cycle wraps through AutoPilot, so a gesture that only passes through
	// it must not fire a wasted LLM call. Confirm the user actually rested here
	// (handleAutopilotModeSettled).
	if m.env.OperationMode == setting.ModeAutoPilot {
		return tea.Tick(autopilotSettleDelay, func(time.Time) tea.Msg { return autopilotModeSettledMsg{} })
	}
	return nil
}

// applyOperationMode re-applies the current mode's permission posture and hook
// mode — the shared tail of any mode change.
func (m *model) applyOperationMode() {
	m.env.ApplyModePermissions(m.env.CWD)
	m.services.Hook.SetPermissionMode(m.env.OperationModeName())
}

func (m *model) persistOperationMode() {
	if err := setting.UpdateLastOperationMode(m.env.OperationMode); err != nil {
		log.Logger().Warn("persist operation mode", zap.Error(err))
	}
}

// enterAutoPilotMode switches the operation posture to AutoPilot without cycling,
// so the /autopilot panel's Start button engages the copilot even from another
// mode (otherwise the kick's autopilotEngaged() gate would drop it).
func (m *model) enterAutoPilotMode() {
	m.env.OperationMode = setting.ModeAutoPilot
	m.applyOperationMode()
	m.persistOperationMode()
}

// autopilotSettleDelay is how long the mode must rest on AutoPilot before the
// kick/suggest fires — long enough to skip a pass-through cycle, short enough to
// feel immediate when the user deliberately stops here.
const autopilotSettleDelay = 250 * time.Millisecond

// autopilotModeSettledMsg fires autopilotSettleDelay after landing on AutoPilot.
type autopilotModeSettledMsg struct{}

// handleAutopilotModeSettled surfaces the Suggest steer's opening proposal once
// the mode has rested on AutoPilot. A mission waiting to start or resume shows
// its offer instead (see missionOfferVisible), so no proposal is fetched. It
// no-ops if the user has since cycled away, so a pass-through cycle costs
// nothing.
//
// A mid-turn landing skips it: the proposal is ghost text for an idle textarea
// (HandlePromptSuggestion drops it while streaming anyway), and OnTurnEnd
// re-arms the hint once the turn is over.
func (m *model) handleAutopilotModeSettled() tea.Cmd {
	if !m.autopilotEngaged() || m.conv.Stream.Active || m.missionOfferVisible() {
		return nil
	}
	return m.startPromptSuggestion()
}

func (m *model) updateMode(msg tea.Msg) (tea.Cmd, bool) {
	if msg, ok := msg.(conv.InteractionRequestMsg); ok {
		return m.enqueueInteraction(msg.Request), true
	}
	return nil, false
}

func (m *model) handleQuestionResponse(msg conv.QuestionResponseMsg) tea.Cmd {
	req := m.interactions.active
	if !m.validInteraction(req) || req.Question == nil || req.Question.Request != msg.Request {
		return nil
	}
	response := msg.Response
	if msg.Cancelled {
		response = &tool.QuestionResponse{RequestID: msg.Request.ID, Cancelled: true}
	}
	if response == nil || response.RequestID != msg.Request.ID {
		return nil
	}
	select {
	case req.Question.Reply <- response:
	default:
	}
	m.finishInteraction()
	return m.advanceInteractions()
}

func (m *model) handleSecretPromptResponse(msg input.SecretPromptResponseMsg) tea.Cmd {
	req := m.interactions.active
	if !m.validInteraction(req) || req.Secret == nil || req.ID != msg.RequestID {
		return nil
	}
	select {
	case req.Secret.Reply <- conv.SecretPromptResponse{Value: msg.Value, Cancelled: msg.Cancelled}:
	default:
	}
	m.finishInteraction()
	return m.advanceInteractions()
}
