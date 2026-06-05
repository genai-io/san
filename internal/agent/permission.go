package agent

import (
	"context"

	"github.com/genai-io/san/v2/internal/tool/perm"
)

// PermDecisionResult holds a permission decision and its reason.
//
// RequestID is set by the decider when Decision == perm.Prompt so the
// matching permission.required and permission.decided audit records can be
// joined. It flows through PermBridgeRequest to the resolver (TUI), which
// passes it back unchanged when the user/hook decision lands.
type PermDecisionResult struct {
	Decision    perm.Decision
	Reason      string
	ToolName    string
	Description string
	RequestID   string
}

// PermDecisionFunc evaluates whether a tool call is allowed, denied, or needs prompting.
type PermDecisionFunc func(name string, args map[string]any) PermDecisionResult

// PermBridgeRequest is a pending permission request sent to the TUI for approval.
//
// RequestID carries the correlation token the decider stamped so the TUI
// can reference the prior permission.required record when emitting
// permission.decided.
type PermBridgeRequest struct {
	RequestID   string
	ToolName    string
	Description string
	Input       map[string]any
	Response    chan PermBridgeResponse
}

// PermBridgeResponse is the user's decision on a permission request.
type PermBridgeResponse struct {
	Allow  bool
	Reason string
}

// PermissionBridge gates tool execution by routing permission decisions
// through a channel pair. The agent side blocks on the response; the TUI
// side receives requests and sends back decisions.
type PermissionBridge struct {
	requests chan *PermBridgeRequest
	decideFn PermDecisionFunc
}

func NewPermissionBridge(decideFn PermDecisionFunc) *PermissionBridge {
	return &PermissionBridge{
		requests: make(chan *PermBridgeRequest, 1),
		decideFn: decideFn,
	}
}

func (pb *PermissionBridge) PermissionFunc() perm.PermissionFunc {
	return func(ctx context.Context, name string, input map[string]any) (bool, string) {
		decision := pb.decideFn(name, input)

		switch decision.Decision {
		case perm.Permit:
			return true, decision.Reason
		case perm.Reject:
			return false, decision.Reason
		}

		req := &PermBridgeRequest{
			RequestID:   decision.RequestID,
			ToolName:    decision.ToolName,
			Description: decision.Description,
			Input:       input,
			Response:    make(chan PermBridgeResponse, 1),
		}

		select {
		case pb.requests <- req:
		case <-ctx.Done():
			return false, "cancelled"
		}

		select {
		case <-ctx.Done():
			return false, "cancelled"
		case resp := <-req.Response:
			return resp.Allow, resp.Reason
		}
	}
}

func (pb *PermissionBridge) Recv() (*PermBridgeRequest, bool) {
	req, ok := <-pb.requests
	return req, ok
}

func (pb *PermissionBridge) Close() {
	close(pb.requests)
}
