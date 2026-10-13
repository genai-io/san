package subagent

import (
	"context"
	"testing"

	"github.com/genai-io/san/internal/tool"
)

func TestInteractionPromptDoesNotOverrideHardRejects(t *testing.T) {
	for _, tc := range []struct {
		name        string
		mode        PermissionMode
		allow, deny ToolList
		tool        string
		input       map[string]any
	}{
		{name: "deny", mode: PermissionDefault, deny: ToolList{{Name: "Bash"}}, tool: tool.ToolBash, input: map[string]any{"command": "make"}},
		{name: "constraint", mode: PermissionDefault, allow: ToolList{{Name: "Bash", Pattern: "git diff*"}}, tool: tool.ToolBash, input: map[string]any{"command": "make"}},
		{name: "read only", mode: PermissionExplore, tool: tool.ToolBash, input: map[string]any{"command": "make"}},
		{name: "breaker", mode: PermissionBypass, tool: tool.ToolBash, input: map[string]any{"command": "rm -rf /"}},
		{name: "parent only", mode: PermissionBypass, tool: tool.ToolAgent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			gate := subagentPermissionFuncWithPrompt(tc.mode, tc.allow, tc.deny, func(context.Context, string, map[string]any) (bool, string) {
				called = true
				return true, ""
			})
			allowed, _ := gate(context.Background(), tc.tool, tc.input)
			if allowed || called {
				t.Fatalf("hard rejection overridden: allowed=%v prompted=%v", allowed, called)
			}
		})
	}
}

func TestInteractionPromptDecisionAndBypass(t *testing.T) {
	for _, approved := range []bool{false, true} {
		calls := 0
		prompt := func(context.Context, string, map[string]any) (bool, string) { calls++; return approved, "decision" }
		allowed, reason := subagentPermissionFuncWithPrompt(PermissionDefault, nil, nil, prompt)(context.Background(), tool.ToolBash, map[string]any{"command": "make"})
		if allowed != approved || reason != "decision" || calls != 1 {
			t.Fatalf("decision not preserved: %v %q %d", allowed, reason, calls)
		}
		allowed, _ = subagentPermissionFuncWithPrompt(PermissionBypass, nil, nil, prompt)(context.Background(), tool.ToolBash, map[string]any{"command": "make"})
		if !allowed || calls != 1 {
			t.Fatal("bypass prompted")
		}
	}
}
