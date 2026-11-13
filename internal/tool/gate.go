package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/tool/perm"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
)

// The gate: the only thing between the model's call and the tool.

// Permission refuses a call the checker will not allow.
//
// It is a gate rather than a wrapper around every tool: a decorator has to be
// applied on every path that hands a tool out, and a set whose members differ
// depending on how you asked for them is a set somebody will read the wrong
// way. The loop asks this once per call, and a refusal reaches the model as a
// tool error it may work around.
//
// Nil check gates nothing. The checker itself decides whether safe tools are
// allowed, so deny and ask rules still apply consistently.
func Permission(check perm.PermissionFunc) core.Gate {
	if check == nil {
		return nil
	}
	return func(ctx context.Context, c sdkagent.PreToolContext) (sdkagent.Decision, error) {
		input, _ := core.ParseToolInput(c.Call.Input)
		if allow, reason := check(ctx, c.Call.Name, input); !allow {
			return sdkagent.Decision{Block: true, Reason: reason}, nil
		}
		return sdkagent.Decision{}, nil
	}
}

// HookAwareChecker is the permission check as the hook path needs it: one that
// can be told a hook already asked, and asked whether a hook's "allow" is
// enough on its own.
type HookAwareChecker interface {
	Check(ctx context.Context, name string, input map[string]any, forcePrompt bool, reason string) (bool, string)
	// CanApproveByHook checks eligibility, not approval, and does not invoke hooks.
	// False sends the call through Check even when a hook answered "allow".
	CanApproveByHook(name string, input map[string]any) bool
}

// HookedPermission is Permission with the PreToolUse hooks in front of it.
//
// One gate rather than two chained, because the second half reads the first's
// verdict: a hook's "allow" waives the routine prompt and nothing more, and
// whether the waiver holds is the checker's answer, not the hook's.
//
// A hook may also rewrite the arguments, which comes back as
// Decision.Arguments — the call the tool runs is the one that survived the
// gate, not the one the model sent.
type Resolver interface {
	Resolve(context.Context, string, map[string]any, bool, string) perm.Resolution
}

func HookedPermission(hooks hook.Handler, check HookAwareChecker, record ...perm.RecordDecision) core.Gate {
	if len(record) > 1 {
		return func(context.Context, sdkagent.PreToolContext) (sdkagent.Decision, error) {
			return sdkagent.Decision{}, fmt.Errorf("at most one permission recorder is allowed")
		}
	}
	var recorder perm.RecordDecision
	if len(record) == 1 {
		recorder = record[0]
	}
	if hooks == nil && check == nil && recorder == nil {
		return nil
	}
	return func(ctx context.Context, c sdkagent.PreToolContext) (result sdkagent.Decision, err error) {
		var resolved perm.Resolution
		if recorder != nil {
			if _, ok := check.(Resolver); !ok {
				return result, fmt.Errorf("permission recording requires a structured resolver")
			}
		}
		defer func() {
			if err != nil {
				return
			}
			finish := recorder
			if finish == nil {
				finish = resolved.Finish
			}
			if finish == nil {
				return
			}
			resolved.CallID = c.Call.ID
			if resolved.Decision == "" || resolved.By == "" {
				err = fmt.Errorf("permission resolution is missing authority")
				return
			}
			if resolved.Finish != nil && ctx.Err() != nil {
				resolved.Decision, resolved.Reason = "cancelled", ctx.Err().Error()
				result = sdkagent.Decision{Block: true, Reason: resolved.Reason}
			}
			err = finish(context.WithoutCancel(ctx), resolved)
		}()
		input, _ := core.ParseToolInput(c.Call.Input)
		edited := false
		allowByHook, forceAsk, permissionReason := false, false, ""

		if hooks != nil && hooks.HasHooks(hook.PreToolUse) {
			out := hooks.Execute(ctx, hook.PreToolUse, hook.HookInput{
				ToolName:  c.Call.Name,
				ToolInput: input,
			})
			var recording *hook.RecordingError
			if errors.As(out.Error, &recording) {
				return sdkagent.Decision{}, out.Error
			}
			if !out.ShouldContinue || out.ShouldBlock {
				resolved = perm.Resolution{Decision: "deny", By: "hook", Reason: blockReason(out)}
				return sdkagent.Decision{Block: true, Reason: blockReason(out)}, nil
			}
			if out.UpdatedInput != nil {
				input, edited = out.UpdatedInput, true
			}
			allowByHook, forceAsk, permissionReason = out.PermissionAllow, out.ForceAsk, out.PermissionReason
		}

		if check != nil {
			// A hook "allow" waives the routine prompt and nothing more. A hook
			// "ask" outranks it, because prompting is the safe resolution when
			// two hooks disagree; so does a deny rule, the circuit breaker,
			// either confirmation tier or an explicit ask rule, which is what
			// CanApproveByHook reports on.
			waived := allowByHook && !forceAsk && check.CanApproveByHook(c.Call.Name, input)
			if waived {
				resolved = perm.Resolution{Decision: "allow", By: "hook", Reason: permissionReason}
			}
			if !waived {
				if resolver, ok := check.(Resolver); ok {
					resolved = resolver.Resolve(ctx, c.Call.Name, input, forceAsk, permissionReason)
					if resolved.Err != nil {
						return sdkagent.Decision{}, resolved.Err
					}
					if resolved.Decision != "allow" {
						return sdkagent.Decision{Block: true, Reason: resolved.Reason}, nil
					}
				} else if allow, reason := check.Check(ctx, c.Call.Name, input, forceAsk, permissionReason); !allow {
					return sdkagent.Decision{Block: true, Reason: reason}, nil
				}
			}
		}

		if !edited {
			return sdkagent.Decision{}, nil
		}
		rewritten, err := json.Marshal(input)
		if err != nil {
			// The hook's edit cannot be expressed, and running the original
			// would run what the hook rejected. Refuse instead.
			resolved.Decision, resolved.By, resolved.Reason = "deny", "hook", "the hook's arguments could not be encoded"
			return sdkagent.Decision{Block: true, Reason: "the hook's arguments could not be encoded"}, nil
		}
		return sdkagent.Decision{Arguments: string(rewritten)}, nil
	}
}

func blockReason(out hook.HookOutcome) string {
	for _, s := range []string{out.BlockReason, out.AdditionalContext} {
		if s != "" {
			return s
		}
	}
	return "blocked by PreToolUse hook"
}
