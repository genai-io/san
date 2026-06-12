package tool

import (
	"context"
	"fmt"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/hook"
)

func WithPreToolUseHooks(inner core.Tools, hooks hook.Handler) core.Tools {
	if inner == nil || hooks == nil {
		return inner
	}
	return &preToolHookTools{inner: inner, hooks: hooks}
}

type PreToolPermissionChecker interface {
	Check(ctx context.Context, name string, input map[string]any, forcePrompt bool, reason string) (bool, string)
}

func WithPreToolUseAndPermission(inner core.Tools, hooks hook.Handler, check PreToolPermissionChecker) core.Tools {
	if check == nil {
		return WithPreToolUseHooks(inner, hooks)
	}
	if inner == nil {
		return nil
	}
	return &preToolHookTools{inner: inner, hooks: hooks, check: check}
}

type preToolHookTools struct {
	inner core.Tools
	hooks hook.Handler
	check PreToolPermissionChecker
}

func (pt *preToolHookTools) Get(name string) core.Tool {
	t := pt.inner.Get(name)
	if t == nil {
		return nil
	}
	return &preToolHookTool{inner: t, hooks: pt.hooks, check: pt.check}
}

func (pt *preToolHookTools) All() []core.Tool {
	tools := pt.inner.All()
	out := make([]core.Tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, &preToolHookTool{inner: t, hooks: pt.hooks, check: pt.check})
	}
	return out
}

func (pt *preToolHookTools) Add(tool core.Tool, caller string)     { pt.inner.Add(tool, caller) }
func (pt *preToolHookTools) Remove(name, caller string)            { pt.inner.Remove(name, caller) }
func (pt *preToolHookTools) Schemas() []core.ToolSchema            { return pt.inner.Schemas() }
func (pt *preToolHookTools) SetObserver(fn func(core.ToolsChange)) { pt.inner.SetObserver(fn) }

type preToolHookTool struct {
	inner core.Tool
	hooks hook.Handler
	check PreToolPermissionChecker
}

func (pt *preToolHookTool) Name() string            { return pt.inner.Name() }
func (pt *preToolHookTool) Description() string     { return pt.inner.Description() }
func (pt *preToolHookTool) Schema() core.ToolSchema { return pt.inner.Schema() }

func (pt *preToolHookTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	allowByHook := false
	forceAsk := false
	permissionReason := ""
	if pt.hooks != nil && pt.hooks.HasHooks(hook.PreToolUse) {
		outcome := pt.hooks.Execute(ctx, hook.PreToolUse, hook.HookInput{
			ToolName:  pt.inner.Name(),
			ToolInput: input,
		})
		if !outcome.ShouldContinue || outcome.ShouldBlock {
			reason := outcome.BlockReason
			if reason == "" {
				reason = outcome.AdditionalContext
			}
			if reason == "" {
				reason = "blocked by PreToolUse hook"
			}
			return "", fmt.Errorf("%s", reason)
		}
		if outcome.UpdatedInput != nil {
			input = outcome.UpdatedInput
		}
		allowByHook = outcome.PermissionAllow
		forceAsk = outcome.ForceAsk
		permissionReason = outcome.PermissionReason
	}

	if pt.check != nil && !allowByHook {
		if allow, reason := pt.check.Check(ctx, pt.inner.Name(), input, forceAsk, permissionReason); !allow {
			return "", fmt.Errorf("blocked: %s", reason)
		}
	}
	return pt.inner.Execute(ctx, input)
}
