package app

import (
	"context"
	"testing"

	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/setting"
)

func TestNewBaseModelSyncsRestoredOperationModeToHooks(t *testing.T) {
	cwd := t.TempDir()
	settings := setting.NewData()
	settings.LastOperationMode = "auto-accept"

	engine := hook.NewEngine(setting.NewData(), "", cwd, "")
	var hookMode string
	engine.AddSessionFunctionHook(hook.PreToolUse, "", hook.FunctionHook{
		Callback: func(_ context.Context, input hook.HookInput) (hook.HookOutput, error) {
			hookMode = input.PermissionMode
			return hook.HookOutput{}, nil
		},
	})

	environment := env{SessionPermissions: setting.NewSessionPermissions()}
	applyStartupSettings(&environment, settings, cwd, true, engine)
	engine.Execute(context.Background(), hook.PreToolUse, hook.HookInput{ToolName: "Bash"})

	if environment.OperationMode != setting.ModeAutoAccept {
		t.Fatalf("OperationMode = %v, want %v", environment.OperationMode, setting.ModeAutoAccept)
	}
	if hookMode != "auto" {
		t.Fatalf("hook permission mode = %q, want %q", hookMode, "auto")
	}
}
