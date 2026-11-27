package chat

import (
	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/runtime"
)

type PermGateMsg struct {
	Gate    *runtime.PermissionGate
	Request *runtime.PermGateRequest
}

func PollPermGate(pg *runtime.PermissionGate) tea.Cmd {
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
