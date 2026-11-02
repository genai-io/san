package agent

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestProcessToolResult(t *testing.T) {
	for _, tc := range []struct {
		name   string
		effect any
		want   []string
		file   bool
	}{
		{"Bash", map[string]any{"cwd": "/next"}, []string{"cwd:/next"}, false},
		{"PowerShell", map[string]any{"cwd": "/next"}, []string{"cwd:/next"}, false},
		{"Write", map[string]any{"filePath": "file"}, []string{"reload:file", "touch:file"}, true},
		{"Edit", map[string]any{"filePath": "file"}, []string{"reload:file", "touch:file"}, true},
		{"Read", map[string]any{"file": map[string]any{"filePath": "file"}}, []string{"touch:file"}, false},
		{"Read", map[string]any{"file": map[string]any{"filePath": 42}}, []string{"touch:42"}, false},
		{"Read", map[string]any{"filePath": "file"}, nil, false},
		{"Write", map[string]any{"filePath": nil}, nil, false},
		{"Other", map[string]any{"cwd": "/next"}, nil, false},
		{"Other", "structured response", nil, false},
		{"Other", nil, nil, false},
	} {
		for _, failed := range []bool{false, true} {
			t.Run(tc.name+"/"+map[bool]string{true: "failure", false: "success"}[failed], func(t *testing.T) {
				engine := hook.NewEngine(setting.NewData(), "session", "/original", "transcript")
				var effects []string
				received := make(chan hook.HookInput, 3)
				audits := make(chan hook.HookFiredAudit, 3)
				engine.SetAuditCallback(func(a hook.HookFiredAudit) { audits <- a })
				for _, event := range []hook.EventType{hook.PostToolUse, hook.PostToolUseFailure, hook.FileChanged} {
					engine.AddSessionFunctionHook(event, "", hook.FunctionHook{Callback: func(_ context.Context, in hook.HookInput) (hook.HookOutput, error) {
						// Only PostTool is ordered after synchronous effects. FileChanged is detached.
						if in.HookEventName != string(hook.FileChanged) && !reflect.DeepEqual(effects, tc.want) {
							t.Errorf("effects before PostTool = %v, want %v", effects, tc.want)
						}
						received <- in
						return hook.HookOutput{}, errors.New("hook failed")
					}})
				}
				tr := core.ToolResult{ToolCallID: "call", ToolName: tc.name, Content: ai.TextContent("result text"), IsError: failed}
				ProcessToolResult(tr, tc.effect, engine, ToolResultEffects{
					ChangeCwd:      func(p string) { effects = append(effects, "cwd:"+p); engine.SetCwd(p) },
					ReloadPersonas: func(p string) { effects = append(effects, "reload:"+p) },
					TouchFile:      func(p string) { effects = append(effects, "touch:"+p) },
				})
				count := 1
				if tc.file {
					count++
				}
				for range count {
					var in hook.HookInput
					select {
					case in = <-received:
					case <-time.After(5 * time.Second):
						t.Fatal("hook did not run")
					}
					if in.SessionID != "session" || in.TranscriptPath != "transcript" {
						t.Fatalf("identity = %+v", in)
					}
					if in.HookEventName == string(hook.FileChanged) {
						if in.FilePath != "file" || in.Source != tc.name || in.Event != "change" {
							t.Fatalf("file input = %+v", in)
						}
						continue
					}
					event := hook.PostToolUse
					wantError := ""
					if failed {
						event = hook.PostToolUseFailure
						wantError = "result text"
					}
					response := tc.effect
					if response == nil {
						response = tr.Content
					}
					if in.HookEventName != string(event) || in.ToolName != tc.name || in.ToolUseID != "call" || in.Error != wantError || !reflect.DeepEqual(in.ToolResponse, response) {
						t.Fatalf("post input = %+v", in)
					}
					wantCwd := "/original"
					if tc.name == "Bash" || tc.name == "PowerShell" {
						wantCwd = "/next"
					}
					if in.Cwd != wantCwd {
						t.Fatalf("cwd = %q, want %q", in.Cwd, wantCwd)
					}
				}
				for range count {
					select {
					case a := <-audits:
						if a.Outcome != "error" || a.Reason != "hook failed" {
							t.Fatalf("audit = %+v", a)
						}
					case <-time.After(5 * time.Second):
						t.Fatal("missing audit")
					}
				}
				select {
				case in := <-received:
					t.Fatalf("unexpected hook: %+v", in)
				default:
				}
			})
		}
	}
}
