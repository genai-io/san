package conv

import (
	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/agent"
)

type PermGateMsg struct {
	Gate    *agent.PermissionGate
	Request *agent.PermGateRequest
}

func PollPermGate(pg *agent.PermissionGate) tea.Cmd {
	if pg == nil {
		return nil
	}
	return func() tea.Msg {
		req, ok := pg.Recv()
		if !ok {
			return nil
		}
		return PermGateMsg{Gate: pg, Request: req}
	}
}
