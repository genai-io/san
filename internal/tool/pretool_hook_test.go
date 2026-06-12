package tool

import (
	"context"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/hook"
)

type fakeCoreTool struct {
	name     string
	executed bool
	input    map[string]any
	execute  func(ctx context.Context, input map[string]any) (string, error)
}

func (t *fakeCoreTool) Name() string            { return t.name }
func (t *fakeCoreTool) Description() string     { return "test" }
func (t *fakeCoreTool) Schema() core.ToolSchema { return core.ToolSchema{Name: t.name} }
func (t *fakeCoreTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	t.executed = true
	t.input = input
	if t.execute != nil {
		return t.execute(ctx, input)
	}
	return "ok", nil
}

type fakeHookHandler struct {
	outcomeFor func(input hook.HookInput) hook.HookOutcome
	inputs     []hook.HookInput
}

func (h *fakeHookHandler) Execute(ctx context.Context, event hook.EventType, input hook.HookInput) hook.HookOutcome {
	h.inputs = append(h.inputs, input)
	return h.outcomeFor(input)
}
func (h *fakeHookHandler) ExecuteAsync(event hook.EventType, input hook.HookInput) {}
func (h *fakeHookHandler) HasHooks(event hook.EventType) bool                      { return event == hook.PreToolUse }
func (h *fakeHookHandler) StopHookActive() *bool                                   { return nil }

func staticOutcome(outcome hook.HookOutcome) *fakeHookHandler {
	return &fakeHookHandler{outcomeFor: func(hook.HookInput) hook.HookOutcome { return outcome }}
}

func TestPreToolUseUpdatedInputSeenByCheckAndTool(t *testing.T) {
	inner := &fakeCoreTool{name: "Bash"}
	hooks := staticOutcome(hook.HookOutcome{UpdatedInput: map[string]any{"command": "rtk git status"}})
	var checkedInput map[string]any
	tools := WithPreToolUseHooks(core.NewTools(inner), hooks,
		func(ctx context.Context, name string, input map[string]any) (bool, string) {
			checkedInput = input
			return true, ""
		}, nil)

	if _, err := tools.Get("Bash").Execute(context.Background(), map[string]any{"command": "git status"}); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if got := hooks.inputs[0].ToolInput["command"]; got != "git status" {
		t.Fatalf("hook saw input %v, want original", got)
	}
	if got := checkedInput["command"]; got != "rtk git status" {
		t.Fatalf("permission check saw input %v, want updated", got)
	}
	if got := inner.input["command"]; got != "rtk git status" {
		t.Fatalf("tool executed with input %v, want updated", got)
	}
}

func TestPreToolUseBlockSkipsCheckAndTool(t *testing.T) {
	inner := &fakeCoreTool{name: "Bash"}
	hooks := staticOutcome(hook.HookOutcome{ShouldBlock: true, BlockReason: "not allowed"})
	tools := WithPreToolUseHooks(core.NewTools(inner), hooks,
		func(ctx context.Context, name string, input map[string]any) (bool, string) {
			t.Fatal("check should not run when hook blocks")
			return true, ""
		}, nil)

	_, err := tools.Get("Bash").Execute(context.Background(), map[string]any{"command": "rm -rf /"})
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("Execute error = %v, want block reason", err)
	}
	if inner.executed {
		t.Fatal("tool should not execute when hook blocks")
	}
}

func TestPreToolUseAllowSkipsCheck(t *testing.T) {
	inner := &fakeCoreTool{name: "Bash"}
	hooks := staticOutcome(hook.HookOutcome{PermissionAllow: true})
	tools := WithPreToolUseHooks(core.NewTools(inner), hooks,
		func(ctx context.Context, name string, input map[string]any) (bool, string) {
			t.Fatal("check should not run after hook allow")
			return false, ""
		}, nil)

	if _, err := tools.Get("Bash").Execute(context.Background(), map[string]any{"command": "git status"}); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !inner.executed {
		t.Fatal("tool should execute after hook allow")
	}
}

func TestPreToolUseForceAskRoutesToPrompt(t *testing.T) {
	inner := &fakeCoreTool{name: "Bash"}
	// "ask" wins even when another hook answered "allow" for the same call.
	hooks := staticOutcome(hook.HookOutcome{ForceAsk: true, PermissionAllow: true})
	var promptedReason string
	allowNext := true
	tools := WithPreToolUseHooks(core.NewTools(inner), hooks,
		func(ctx context.Context, name string, input map[string]any) (bool, string) {
			t.Fatal("check should not run when hook forces a prompt")
			return false, ""
		},
		func(ctx context.Context, name string, input map[string]any, reason string) (bool, string) {
			promptedReason = reason
			return allowNext, "denied by user"
		})

	if _, err := tools.Get("Bash").Execute(context.Background(), map[string]any{"command": "git push"}); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !inner.executed {
		t.Fatal("tool should execute after user approves prompt")
	}
	if promptedReason == "" {
		t.Fatal("prompt should receive the hook reason")
	}

	inner.executed = false
	allowNext = false
	_, err := tools.Get("Bash").Execute(context.Background(), map[string]any{"command": "git push"})
	if err == nil || !strings.Contains(err.Error(), "denied by user") {
		t.Fatalf("Execute error = %v, want user denial", err)
	}
	if inner.executed {
		t.Fatal("tool should not execute after user denies prompt")
	}
}

// A hook "allow" for one tool call must not leak into tool calls nested
// under it (e.g. a subagent run sharing the parent's context).
func TestPreToolUseAllowDoesNotLeakIntoNestedCalls(t *testing.T) {
	nestedChecked := false
	nested := &fakeCoreTool{name: "Bash"}
	hooks := &fakeHookHandler{outcomeFor: func(input hook.HookInput) hook.HookOutcome {
		if input.ToolName == "Agent" {
			return hook.HookOutcome{PermissionAllow: true}
		}
		return hook.HookOutcome{}
	}}
	nestedTools := WithPreToolUseHooks(core.NewTools(nested), hooks,
		func(ctx context.Context, name string, input map[string]any) (bool, string) {
			nestedChecked = true
			return false, "nested check ran"
		}, nil)

	outer := &fakeCoreTool{name: "Agent", execute: func(ctx context.Context, input map[string]any) (string, error) {
		// Simulates a subagent executing its own tools with the parent ctx.
		return nestedTools.Get("Bash").Execute(ctx, map[string]any{"command": "git push"})
	}}
	outerTools := WithPreToolUseHooks(core.NewTools(outer), hooks,
		func(ctx context.Context, name string, input map[string]any) (bool, string) {
			return true, ""
		}, nil)

	_, err := outerTools.Get("Agent").Execute(context.Background(), map[string]any{"prompt": "do it"})
	if !nestedChecked {
		t.Fatal("nested permission check must run despite outer hook allow")
	}
	if err == nil || !strings.Contains(err.Error(), "nested check ran") {
		t.Fatalf("nested Execute error = %v, want nested check denial", err)
	}
	if nested.executed {
		t.Fatal("nested tool should not execute when its check denies")
	}
}

func TestPreToolUseNilHooksFallsBackToPermission(t *testing.T) {
	inner := &fakeCoreTool{name: "Bash"}
	tools := WithPreToolUseHooks(core.NewTools(inner), nil,
		func(ctx context.Context, name string, input map[string]any) (bool, string) {
			return false, "checked"
		}, nil)

	_, err := tools.Get("Bash").Execute(context.Background(), map[string]any{"command": "git push"})
	if err == nil || !strings.Contains(err.Error(), "checked") {
		t.Fatalf("Execute error = %v, want permission denial", err)
	}
}
