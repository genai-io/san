package agent

import (
	"context"

	"github.com/genai-io/san/internal/tool/perm"
)

// PermDecisionResult holds a permission decision and its reason.
//
// RequestID is set by the decider when Decision == perm.Prompt so the
// matching permission.required and permission.decided audit records can be
// joined. It flows through PermGateRequest to the resolver (TUI), which
// passes it back unchanged when the user/hook decision lands.
type PermDecisionResult struct {
	Decision    perm.Decision
	Reason      string
	ToolName    string
	Description string
	RequestID   string
	// Reviewable marks a Prompt the auto-review agent may judge instead of the
	// user. Set only for the auto-review gray-zone default.
	Reviewable bool
}

// PermDecisionFunc evaluates whether a tool call is allowed, denied, or needs prompting.
type PermDecisionFunc func(name string, args map[string]any) PermDecisionResult

// PermReviewResult is the outcome of a gray-zone review. Allow=true auto-approves
// the call; the zero value (Allow=false) escalates it to the human.
type PermReviewResult struct {
	Allow  bool
	Reason string
}

// PermReviewFunc judges a reviewable gray-zone tool call. It runs on the agent
// goroutine and must fail closed (return the zero value) on any error, so a
// broken or slow judge can never silently approve.
type PermReviewFunc func(ctx context.Context, name string, input map[string]any, reason string) PermReviewResult

// PermGateRequest is a pending permission request sent to the TUI for approval.
//
// RequestID carries the correlation token the decider stamped so the TUI
// can reference the prior permission.required record when emitting
// permission.decided.
type PermGateRequest struct {
	RequestID   string
	ToolName    string
	Description string
	Input       map[string]any
	Response    chan PermGateResponse
}

// PermGateResponse is the user's decision on a permission request.
type PermGateResponse struct {
	Allow  bool
	Reason string
}

// PermissionGate gates tool execution by routing permission decisions
// through a channel pair. The agent side blocks on the response; the TUI
// side receives requests and sends back decisions.
type PermissionGate struct {
	requests chan *PermGateRequest
	decideFn PermDecisionFunc
	reviewFn PermReviewFunc // optional; judges reviewable gray-zone prompts
}

func NewPermissionGate(decideFn PermDecisionFunc) *PermissionGate {
	return &PermissionGate{
		requests: make(chan *PermGateRequest, 1),
		decideFn: decideFn,
	}
}

// SetReviewer installs the gray-zone judge. When set, a reviewable Prompt is
// offered to it before falling back to the user. A nil fn disables review.
func (pg *PermissionGate) SetReviewer(fn PermReviewFunc) {
	pg.reviewFn = fn
}

func (pg *PermissionGate) PermissionFunc() perm.PermissionFunc {
	return func(ctx context.Context, name string, input map[string]any) (bool, string) {
		return pg.Check(ctx, name, input, false, "")
	}
}

func (pg *PermissionGate) Check(ctx context.Context, name string, input map[string]any, forcePrompt bool, reason string) (bool, string) {
	// When a hook forces a prompt we skip decideFn entirely: its result is
	// discarded anyway, and calling it would emit a misleading "decided"
	// audit record for a call that actually goes to the user.
	if forcePrompt {
		return pg.prompt(ctx, &PermGateRequest{ToolName: name, Description: reason, Input: input})
	}

	decision := pg.decideFn(name, input)

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

	// Gray-zone review: offer a reviewable Prompt to the judge before the user.
	// Allow short-circuits; anything else falls through to the human prompt.
	if decision.Reviewable && pg.reviewFn != nil {
		if rv := pg.reviewFn(ctx, name, input, decision.Reason); rv.Allow {
			return true, rv.Reason
		}
	}

	return pg.prompt(ctx, &PermGateRequest{
		RequestID:   decision.RequestID,
		ToolName:    decision.ToolName,
		Description: decision.Description,
		Input:       input,
	})
}

// prompt sends a permission request to the resolver (TUI) and blocks until
// it responds or ctx is cancelled.
func (pg *PermissionGate) prompt(ctx context.Context, req *PermGateRequest) (bool, string) {
	req.Response = make(chan PermGateResponse, 1)

	select {
	case pg.requests <- req:
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

func (pg *PermissionGate) Recv() (*PermGateRequest, bool) {
	req, ok := <-pg.requests
	return req, ok
}

func (pg *PermissionGate) Close() {
	close(pg.requests)
}
