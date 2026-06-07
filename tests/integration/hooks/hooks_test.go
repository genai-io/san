package hooks_test

import (
	"context"
	"runtime"
	"testing"

	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/setting"
)

func TestHooks_BlockToolCall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows (no sh)")
	}

	settings := &setting.Data{
		Hooks: map[string][]setting.Hook{
			"PreToolUse": {
				{
					Matcher: "Bash",
					Hooks: []setting.HookCmd{
						{Type: "command", Command: "echo 'blocked' >&2; exit 2"},
					},
				},
			},
		},
	}

	engine := hook.NewEngine(settings, "test-session", t.TempDir(), "")

	input := hook.HookInput{
		ToolName:  "Bash",
		ToolInput: map[string]any{"command": "ls"},
		ToolUseID: "tc1",
	}

	outcome := engine.Execute(context.Background(), hook.PreToolUse, input)

	if !outcome.ShouldBlock {
		t.Error("expected hook to block execution")
	}
	if outcome.BlockReason == "" {
		t.Error("expected non-empty block reason")
	}
}

func TestHooks_ModifyToolInput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows (no sh)")
	}

	settings := &setting.Data{
		Hooks: map[string][]setting.Hook{
			"PreToolUse": {
				{
					Matcher: "Read",
					Hooks: []setting.HookCmd{
						{Type: "command", Command: `echo '{"hookSpecificOutput":{"hookEventName":"PreToolUse","updatedInput":{"file_path":"/modified"}}}'`},
					},
				},
			},
		},
	}

	engine := hook.NewEngine(settings, "test-session", t.TempDir(), "")

	input := hook.HookInput{
		ToolName:  "Read",
		ToolInput: map[string]any{"file_path": "/original"},
		ToolUseID: "tc1",
	}

	outcome := engine.Execute(context.Background(), hook.PreToolUse, input)

	if outcome.ShouldBlock {
		t.Error("should not block")
	}
	if outcome.UpdatedInput == nil {
		t.Fatal("expected updated input")
	}
	if outcome.UpdatedInput["file_path"] != "/modified" {
		t.Errorf("expected modified path '/modified', got %v", outcome.UpdatedInput["file_path"])
	}
}

func TestHooks_NoHooks_PassThrough(t *testing.T) {
	// No hooks configured
	engine := hook.NewEngine(&setting.Data{}, "test-session", t.TempDir(), "")

	input := hook.HookInput{
		ToolName:  "Read",
		ToolInput: map[string]any{"file_path": "/test"},
		ToolUseID: "tc1",
	}

	outcome := engine.Execute(context.Background(), hook.PreToolUse, input)

	if outcome.ShouldBlock {
		t.Error("no hooks should mean no blocking")
	}
	if !outcome.ShouldContinue {
		t.Error("should continue when no hooks configured")
	}
}

func TestHooks_NilSettings(t *testing.T) {
	engine := hook.NewEngine(nil, "test-session", t.TempDir(), "")

	if engine.HasHooks(hook.PreToolUse) {
		t.Error("nil settings should have no hooks")
	}

	outcome := engine.Execute(context.Background(), hook.PreToolUse, hook.HookInput{})
	if outcome.ShouldBlock {
		t.Error("nil settings should not block")
	}
}

func TestHooks_HasHooks(t *testing.T) {
	settings := &setting.Data{
		Hooks: map[string][]setting.Hook{
			"PreToolUse": {
				{Hooks: []setting.HookCmd{{Command: "echo ok"}}},
			},
		},
	}

	engine := hook.NewEngine(settings, "test-session", t.TempDir(), "")

	if !engine.HasHooks(hook.PreToolUse) {
		t.Error("expected HasHooks=true for PreToolUse")
	}
	if engine.HasHooks(hook.PostToolUse) {
		t.Error("expected HasHooks=false for PostToolUse")
	}
}
