package agent

import (
	"context"

	"github.com/genai-io/san/internal/tool/perm"
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
		return pb.Check(ctx, name, input, false, "")
	}
}

func (pb *PermissionBridge) Check(ctx context.Context, name string, input map[string]any, forcePrompt bool, reason string) (bool, string) {
	// When a hook forces a prompt we skip decideFn entirely: its result is
	// discarded anyway, and calling it would emit a misleading "decided"
	// audit record for a call that actually goes to the user.
	if forcePrompt {
		return pb.prompt(ctx, &PermBridgeRequest{ToolName: name, Description: reason, Input: input})
	}

	decision := pb.decideFn(name, input)

	switch decision.Decision {
	case perm.Permit:
		return true, decision.Reason
	case perm.Reject:
		return false, decision.Reason
	}

	if decision.ToolName == "" {
		decision.ToolName = name
	}
	if decision.Description == "" {
		decision.Description = decision.Reason
	}

	return pb.prompt(ctx, &PermBridgeRequest{
		RequestID:   decision.RequestID,
		ToolName:    decision.ToolName,
		Description: decision.Description,
		Input:       input,
	})
}

// prompt sends a permission request to the resolver (TUI) and blocks until
// it responds or ctx is cancelled.
func (pb *PermissionBridge) prompt(ctx context.Context, req *PermBridgeRequest) (bool, string) {
	req.Response = make(chan PermBridgeResponse, 1)

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

func (pb *PermissionBridge) Recv() (*PermBridgeRequest, bool) {
	req, ok := <-pb.requests
	return req, ok
}

func (pb *PermissionBridge) Close() {
	close(pb.requests)
}
