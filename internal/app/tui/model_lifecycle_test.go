package tui

import (
	"context"
	"testing"

	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/setting"
)

func TestApplyStartupSettingsPermissionMode(t *testing.T) {
	for _, tt := range []struct {
		name         string
		lastMode     string
		defaultMode  string
		lockBypass   bool
		wantMode     setting.OperationMode
		wantHookMode string
	}{
		{name: "defaults to YOLO", wantMode: setting.ModeBypassPermissions, wantHookMode: "bypassPermissions"},
		{name: "locked bypass falls back to normal", lockBypass: true, wantMode: setting.ModeNormal, wantHookMode: "default"},
		{name: "configured normal overrides YOLO", defaultMode: "normal", wantMode: setting.ModeNormal, wantHookMode: "default"},
		{name: "restored mode wins over configured default", lastMode: "auto-accept", defaultMode: "normal", wantMode: setting.ModeAutoAccept, wantHookMode: "auto"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cwd := t.TempDir()
			settings := setting.NewData()
			settings.LastOperationMode = tt.lastMode
			settings.Permissions.DefaultMode = tt.defaultMode
			if tt.lockBypass {
				settings.AllowBypass = new(false)
			}
			svc := setting.New(settings)
			engine := hook.NewEngine(setting.NewData(), "", cwd, "")
			var hookMode string
			engine.AddSessionFunctionHook(hook.PreToolUse, "", hook.FunctionHook{
				Callback: func(_ context.Context, input hook.HookInput) (hook.HookOutput, error) {
					hookMode = input.PermissionMode
					return hook.HookOutput{}, nil
				},
			})

			environment := env{SessionPermissions: setting.NewSessionPermissions()}
			applyStartupSettings(&environment, svc.Snapshot(), cwd, svc.AllowBypass(), engine)
			engine.Execute(context.Background(), hook.PreToolUse, hook.HookInput{ToolName: "Bash"})

			if environment.OperationMode != tt.wantMode {
				t.Errorf("OperationMode = %v, want %v", environment.OperationMode, tt.wantMode)
			}
			if got := environment.SessionPermissions.CurrentMode(); got != tt.wantMode {
				t.Errorf("session permission mode = %v, want %v", got, tt.wantMode)
			}
			if hookMode != tt.wantHookMode {
				t.Errorf("hook permission mode = %q, want %q", hookMode, tt.wantHookMode)
			}
		})
	}
}
