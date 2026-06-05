package conv

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/genai-io/san/v2/internal/agent"
)

// Re-export agent permission types for conv consumers.
type (
	PermDecisionResult = agent.PermDecisionResult
	PermDecisionFunc   = agent.PermDecisionFunc
	PermBridgeRequest  = agent.PermBridgeRequest
	PermBridgeResponse = agent.PermBridgeResponse
	PermissionBridge   = agent.PermissionBridge
)

var NewPermissionBridge = agent.NewPermissionBridge

type PermBridgeMsg struct {
	Request *PermBridgeRequest
}

func PollPermBridge(pb *PermissionBridge) tea.Cmd {
	return func() tea.Msg {
		req, ok := pb.Recv()
		if !ok {
			return nil
		}
		return PermBridgeMsg{Request: req}
	}
}
