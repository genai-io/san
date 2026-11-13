package agent

import (
	"context"
	"slices"
	"sync/atomic"

	"github.com/genai-io/san/internal/core"
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
	By          string
	Scope       string
	Reason      string
	ToolName    string
	Description string
	RequestID   string
	// Judgeable marks a Prompt the judge may judge instead of the
	// user. Set only for the autopilot gray-zone default.
	Judgeable bool
}

// PermDecisionFunc evaluates whether a tool call is allowed, denied, or needs prompting.
type PermDecisionFunc func(name string, args map[string]any) PermDecisionResult

// PermJudgeResult is the outcome of a gray-zone review. Allow=true auto-approves
// the call; the zero value (Allow=false) escalates it to the human.
type PermJudgeResult struct {
	Allow  bool
	Reason string
}

// PermJudgeFunc judges a judgeable gray-zone tool call. It runs on the agent
// goroutine and must fail closed (return the zero value) on any error, so a
// broken or slow judge can never silently approve.
type PermJudgeFunc func(ctx context.Context, name string, input map[string]any, reason string) PermJudgeResult

// PermHookApprovalFunc checks eligibility for a PreToolUse hook's approval;
// it neither invokes a hook nor approves the call. Precedence is deny rules >
// safety checks (circuit breaker and confirmation tiers) > explicit ask > hook approval.
type PermHookApprovalFunc func(name string, args map[string]any) bool

// PermGateRequest is a pending permission request sent to the TUI for approval.
//
// RequestID carries the correlation token the decider stamped so the TUI
// can reference the prior permission.required record when emitting
// permission.decided.
//
// ToolCallID names the call this request gates. The TUI needs it because a
// call is already stamped as started by its PreToolEvent, which fires before
// the request goes out: without the ID no view can tell the call waiting on
// the user apart from a batch sibling that really is executing.
type PermGateRequest struct {
	// Context ends when the caller cancels, the gate closes, or the prompt returns.
	// Resolvers can use it to retire requests that no longer await a decision.
	Context     context.Context
	RequestID   string
	ToolCallID  string
	ToolName    string
	Description string
	Input       map[string]any
	Response    chan PermGateResponse
	asked       atomic.Pointer[perm.Asked]
}

// MarkAsked retains what was shown even if cancellation wins the response race.
func (r *PermGateRequest) MarkAsked(asked perm.Asked) {
	asked.Options = slices.Clone(asked.Options)
	asked.Detail = slices.Clone(asked.Detail)
	r.asked.Store(&asked)
}

// PermGateResponse is the user's decision on a permission request.
type PermGateResponse struct {
	By        string
	Scope     string
	Asked     *perm.Asked
	Cancelled bool
	Allow     bool
	Reason    string
}

// PermissionGate gates tool execution by routing permission decisions
// through a channel pair. The agent side blocks on the response; the TUI
// side receives requests and sends back decisions.
type PermissionGate struct {
	ctx            context.Context
	cancel         context.CancelFunc
	requests       chan *PermGateRequest
	decideFn       PermDecisionFunc
	judgeFn        PermJudgeFunc        // optional; judges judgeable gray-zone prompts
	hookAllowCheck PermHookApprovalFunc // optional; checks hook approval eligibility
}

func NewPermissionGate(decideFn PermDecisionFunc) *PermissionGate {
	ctx, cancel := context.WithCancel(context.Background())
	return &PermissionGate{
		ctx:      ctx,
		cancel:   cancel,
		requests: make(chan *PermGateRequest, 1),
		decideFn: decideFn,
	}
}

// SetJudge installs the gray-zone judge. When set, a judgeable Prompt is
// offered to it before falling back to the user. A nil fn disables review.
func (pg *PermissionGate) SetJudge(fn PermJudgeFunc) {
	pg.judgeFn = fn
}

// SetHookAllowCheck installs the hook approval eligibility check. Nil fails closed.
func (pg *PermissionGate) SetHookAllowCheck(fn PermHookApprovalFunc) {
	pg.hookAllowCheck = fn
}

// CanApproveByHook checks eligibility for hook approval without invoking a hook
// or approving the call. A gate with no check fails closed.
func (pg *PermissionGate) CanApproveByHook(name string, input map[string]any) bool {
	if pg.hookAllowCheck == nil {
		return false
	}
	return pg.hookAllowCheck(name, input)
}

// WithRules shares the prompt receiver but uses call-scoped authorization rules.
// The original gate remains unchanged for concurrent calls in other scopes.
func (pg *PermissionGate) WithRules(decide PermDecisionFunc) *PermissionGate {
	scoped := *pg
	scoped.decideFn = decide
	return &scoped
}

func (pg *PermissionGate) PermissionFunc() perm.PermissionFunc {
	return func(ctx context.Context, name string, input map[string]any) (bool, string) {
		return pg.Check(ctx, name, input, false, "")
	}
}

func (pg *PermissionGate) Check(ctx context.Context, name string, input map[string]any, forcePrompt bool, reason string) (bool, string) {
	r := pg.Resolve(ctx, name, input, forcePrompt, reason)
	return r.Decision == "allow", r.Reason
}

// Resolve returns actual authority and scope without interpreting reason text.
func (pg *PermissionGate) Resolve(ctx context.Context, name string, input map[string]any, forcePrompt bool, reason string) perm.Resolution {
	if forcePrompt {
		// A forced hook prompt is an explicit policy, so it takes precedence over
		// bypass mode just as it takes precedence over any other permission mode.
		// It is intentionally not a confirmation generated by the permission gate.
		return pg.promptResolution(ctx, &PermGateRequest{ToolName: name, Description: reason, Input: input})
	}

	decision := pg.decideFn(name, input)

	switch decision.Decision {
	case perm.Permit:
		return perm.Resolution{Decision: "allow", By: decision.By, Scope: decision.Scope, Reason: decision.Reason}
	case perm.Reject:
		return perm.Resolution{Decision: "deny", By: decision.By, Scope: decision.Scope, Reason: decision.Reason}
	}

	if decision.ToolName == "" {
		decision.ToolName = name
	}
	if decision.Description == "" {
		decision.Description = decision.Reason
	}

	// Gray-zone review: offer a judgeable Prompt to the judge before the user.
	// Allow short-circuits; anything else falls through to the human prompt.
	if decision.Judgeable && pg.judgeFn != nil {
		if rv := pg.judgeFn(ctx, name, input, decision.Reason); rv.Allow {
			return perm.Resolution{Decision: "allow", By: "judge", Reason: rv.Reason}
		}
	}

	return pg.promptResolution(ctx, &PermGateRequest{
		RequestID:   decision.RequestID,
		ToolName:    decision.ToolName,
		Description: decision.Description,
		Input:       input,
	})
}

// prompt sends a permission request to the resolver (TUI) and blocks until
// it responds, ctx is cancelled, or the gate closes.
//
// The tool call ID is taken from the context the agent stamps before Execute
// (core.WithToolCallID), the only place that knows which call this check runs
// for — the permission func signature carries the tool name, not the call.
func (pg *PermissionGate) prompt(ctx context.Context, req *PermGateRequest) (bool, string) {
	r := pg.promptResolution(ctx, req)
	return r.Decision == "allow", r.Reason
}

func (pg *PermissionGate) promptResolution(ctx context.Context, req *PermGateRequest) perm.Resolution {
	cancelled := func() perm.Resolution {
		return perm.Resolution{Decision: "cancelled", By: "user", Reason: "cancelled", Asked: req.asked.Load()}
	}
	requestCtx, cancel := context.WithCancel(ctx)
	req.Context = requestCtx
	stop := context.AfterFunc(pg.ctx, cancel)
	defer func() {
		stop()
		cancel()
	}()
	// AfterFunc runs asynchronously; reject an already closed gate immediately.
	if pg.ctx.Err() != nil {
		cancel()
	}
	if req.Context.Err() != nil {
		return cancelled()
	}

	req.ToolCallID = core.ToolCallIDFromContext(ctx)
	req.Response = make(chan PermGateResponse, 1)

	select {
	case pg.requests <- req:
	case <-req.Context.Done():
		return cancelled()
	}

	select {
	case <-req.Context.Done():
		return cancelled()
	case resp := <-req.Response:
		if req.Context.Err() != nil || pg.ctx.Err() != nil {
			return cancelled()
		}
		decision := "deny"
		if resp.Cancelled {
			decision = "cancelled"
		} else if resp.Allow {
			decision = "allow"
		}
		by := resp.By
		if by == "" {
			by = "user"
		}
		if resp.Asked == nil {
			resp.Asked = req.asked.Load()
		}
		return perm.Resolution{Decision: decision, By: by, Scope: resp.Scope, Reason: resp.Reason, Asked: resp.Asked}
	}
}

func (pg *PermissionGate) Recv() (*PermGateRequest, bool) {
	return pg.WaitRequest(context.Background())
}

// WaitRequest waits for a permission request until ctx is cancelled or the gate closes.
func (pg *PermissionGate) WaitRequest(ctx context.Context) (*PermGateRequest, bool) {
	if pg.ctx.Err() != nil || ctx.Err() != nil {
		return nil, false
	}
	select {
	case <-ctx.Done():
		return nil, false
	case <-pg.ctx.Done():
		return nil, false
	case req := <-pg.requests:
		if pg.ctx.Err() != nil {
			return nil, false
		}
		return req, true
	}
}

// Close cancels pending prompts and receivers. The producer-facing channel
// stays open so concurrent prompts cannot panic while enqueueing a request.
func (pg *PermissionGate) Close() {
	pg.cancel()
}
