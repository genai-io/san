package conv

import (
	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/agent"
)

// Re-export agent permission types for conv consumers.
type (
	PermDecisionResult = agent.PermDecisionResult
	PermDecisionFunc   = agent.PermDecisionFunc
	PermGateRequest    = agent.PermGateRequest
	PermGateResponse   = agent.PermGateResponse
	PermissionGate     = agent.PermissionGate
)

var NewPermissionGate = agent.NewPermissionGate

type PermGateMsg struct {
	Request *PermGateRequest
}

func PollPermGate(pg *PermissionGate) tea.Cmd {
	return func() tea.Msg {
		req, ok := pg.Recv()
		if !ok {
			return nil
		}
		return PermGateMsg{Request: req}
	}
}
