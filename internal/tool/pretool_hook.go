package tool

import (
	"context"
	"fmt"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/tool/perm"
)

// ForcePromptFunc asks the user to approve a tool call directly, bypassing
// the configured permission decider. Used when a PreToolUse hook answers
// "ask". reason is shown to the user as the prompt description.
type ForcePromptFunc func(ctx context.Context, name string, input map[string]any, reason string) (allow bool, denyReason string)

// WithPreToolUseHooks wraps core.Tools so every Execute runs PreToolUse
// hooks ahead of the permission check. A hook can block the call, rewrite
// its input (the permission check then sees the rewritten input),
// pre-approve it (skips the permission check), or force a user prompt via
// forcePrompt (skips the configured rules and asks directly; falls back to
// the normal check when forcePrompt is nil).
//
// Hook decisions are consumed within this call's stack frame — they are
// never attached to the context — so a decision for one tool call cannot
// leak into nested tool calls such as a subagent run.
func WithPreToolUseHooks(inner core.Tools, hooks hook.Handler, check perm.PermissionFunc, forcePrompt ForcePromptFunc) core.Tools {
	if hooks == nil {
		return WithPermission(inner, check)
	}
	return &preToolHookTools{inner: inner, hooks: hooks, check: check, forcePrompt: forcePrompt}
}

type preToolHookTools struct {
	inner       core.Tools
	hooks       hook.Handler
	check       perm.PermissionFunc
	forcePrompt ForcePromptFunc
}

func (pt *preToolHookTools) Get(name string) core.Tool {
	t := pt.inner.Get(name)
	if t == nil {
		return nil
	}
	return &preToolHookTool{inner: t, gate: pt}
}

func (pt *preToolHookTools) All() []core.Tool                      { return pt.inner.All() }
func (pt *preToolHookTools) Add(tool core.Tool, caller string)     { pt.inner.Add(tool, caller) }
func (pt *preToolHookTools) Remove(name, caller string)            { pt.inner.Remove(name, caller) }
func (pt *preToolHookTools) Schemas() []core.ToolSchema            { return pt.inner.Schemas() }
func (pt *preToolHookTools) SetObserver(fn func(core.ToolsChange)) { pt.inner.SetObserver(fn) }

type preToolHookTool struct {
	inner core.Tool
	gate  *preToolHookTools
}

func (pt *preToolHookTool) Name() string            { return pt.inner.Name() }
func (pt *preToolHookTool) Description() string     { return pt.inner.Description() }
func (pt *preToolHookTool) Schema() core.ToolSchema { return pt.inner.Schema() }

func (pt *preToolHookTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	name := pt.inner.Name()
	if pt.gate.hooks.HasHooks(hook.PreToolUse) {
		outcome := pt.gate.hooks.Execute(ctx, hook.PreToolUse, hook.HookInput{
			ToolName:  name,
			ToolInput: input,
		})
		if outcome.ShouldBlock {
			reason := outcome.BlockReason
			if reason == "" {
				reason = "blocked by PreToolUse hook"
			}
			return "", fmt.Errorf("blocked: %s", reason)
		}
		if outcome.UpdatedInput != nil {
			input = outcome.UpdatedInput
		}
		// ForceAsk takes precedence over PermissionAllow when distinct
		// hooks answered "ask" and "allow" for the same call.
		switch {
		case outcome.ForceAsk && pt.gate.forcePrompt != nil:
			if allow, reason := pt.gate.forcePrompt(ctx, name, input, "requested by PreToolUse hook"); !allow {
				return "", fmt.Errorf("blocked: %s", reason)
			}
			return pt.inner.Execute(ctx, input)
		case outcome.PermissionAllow && !outcome.ForceAsk:
			return pt.inner.Execute(ctx, input)
		}
	}
	if pt.gate.check != nil {
		if allow, reason := pt.gate.check(ctx, name, input); !allow {
			return "", fmt.Errorf("blocked: %s", reason)
		}
	}
	return pt.inner.Execute(ctx, input)
}
