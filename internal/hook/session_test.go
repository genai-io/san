package hook

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/genai-io/san/internal/setting"
)

func TestForSessionSnapshotsConfigurationAndRegistrations(t *testing.T) {
	settings := setting.NewData()
	settings.Hooks[string(Stop)] = []setting.Hook{{Hooks: []setting.HookCmd{{Type: "http", URL: "https://example.invalid", Headers: map[string]string{"x": "original"}, AllowedEnvVars: []string{"A"}}}}}
	parent := NewEngine(settings, "parent", "/parent", "parent.jsonl")
	defer parent.Shutdown()
	parent.SetPermissionMode("plan")
	parent.SetLLMCompleter(func(context.Context, string, string, string) (string, error) { return "legacy", nil }, "model")
	parent.SetEnvProvider(func(context.Context) []string { return []string{"EXTRA=value"} })
	parent.httpClient = &http.Client{Timeout: 7 * time.Second}
	parent.SetRecordedLLMCompleter(func(context.Context, string, string, string) (LLMCompletion, error) {
		t.Error("parent completer leaked")
		return LLMCompletion{}, nil
	}, "model")
	parent.SetCompletionRecorder(func(context.Context, HookFiredAudit) error { t.Error("parent recorder leaked"); return nil })
	parent.SetAuditCallback(func(HookFiredAudit) { t.Error("parent audit leaked") })
	parent.SetAsyncHookCallback(func(AsyncHookResult) { t.Error("parent rewake leaked") })
	parent.store.runtimeHooks[Notification] = []setting.Hook{{Hooks: []setting.HookCmd{{Headers: map[string]string{"x": "runtime"}, AllowedEnvVars: []string{"R"}}}}}
	parent.store.sessionHooks[Setup] = []setting.Hook{{Hooks: []setting.HookCmd{{Headers: map[string]string{"x": "session"}, AllowedEnvVars: []string{"S"}}}}}
	var calls atomic.Int32
	parent.AddRuntimeFunctionHook(UserPromptSubmit, "", FunctionHook{Once: true, Callback: func(_ context.Context, input HookInput) (HookOutput, error) {
		if input.SessionID != "child" || input.Cwd != "/child" || input.TranscriptPath != "child.jsonl" {
			t.Errorf("wrong owner: %+v", input)
		}
		calls.Add(1)
		return HookOutput{}, nil
	}})
	parent.AddSessionFunctionHook(UserPromptSubmit, "", FunctionHook{Once: true, Callback: func(context.Context, HookInput) (HookOutput, error) { calls.Add(1); return HookOutput{}, nil }})
	// The parent's once history must not suppress a child's first invocation.
	parent.store.CheckOnce("parent-only")
	child := parent.ForSession("child", "/child", "child.jsonl")
	defer child.Shutdown()
	if child.recordedCompleter != nil || child.completionRecorder != nil || child.auditCallback != nil || child.asyncCallback != nil {
		t.Fatal("child inherited owner-bound callbacks")
	}
	if child.hookModel != "model" || child.permissionMode != "plan" || child.llmCompleter == nil || child.envProvider(context.Background())[0] != "EXTRA=value" || child.httpClient.Timeout != 7*time.Second {
		t.Fatal("child lost execution configuration")
	}
	if child.httpClient == parent.httpClient || child.status == parent.status || len(child.store.executedOnce) != 0 {
		t.Fatal("child shares mutable execution state")
	}
	settings.Hooks[string(Stop)][0].Hooks[0].Headers["x"] = "changed"
	parent.store.runtimeHooks[Notification][0].Hooks[0].Headers["x"] = "changed"
	parent.store.sessionHooks[Setup][0].Hooks[0].AllowedEnvVars[0] = "changed"
	if child.settings.Hooks[string(Stop)][0].Hooks[0].Headers["x"] != "original" || child.store.runtimeHooks[Notification][0].Hooks[0].Headers["x"] != "runtime" || child.store.sessionHooks[Setup][0].Hooks[0].AllowedEnvVars[0] != "S" {
		t.Fatal("nested hook configuration was not detached")
	}
	parent.ClearSessionHooks()
	parent.AddRuntimeFunctionHook(UserPromptSubmit, "", FunctionHook{Callback: func(context.Context, HookInput) (HookOutput, error) {
		t.Error("late registration leaked")
		return HookOutput{}, nil
	}})
	for range 2 {
		child.Execute(context.Background(), UserPromptSubmit, HookInput{})
	}
	if calls.Load() != 2 {
		t.Fatalf("runtime/session once functions ran %d times", calls.Load())
	}
}

func TestForSessionShutdownIsIndependent(t *testing.T) {
	for _, stopParent := range []bool{false, true} {
		t.Run(map[bool]string{false: "child-first", true: "parent-first"}[stopParent], func(t *testing.T) {
			parent := NewEngine(nil, "parent", t.TempDir(), "")
			defer parent.Shutdown()
			started := make(chan string, 2)
			ended := make(chan string, 2)
			parent.AddRuntimeFunctionHook(Stop, "", FunctionHook{Callback: func(ctx context.Context, input HookInput) (HookOutput, error) {
				started <- input.SessionID
				<-ctx.Done()
				ended <- input.SessionID
				return HookOutput{}, nil
			}})
			child := parent.ForSession("child", parent.cwd, "")
			defer child.Shutdown()
			parent.ExecuteAsync(Stop, HookInput{})
			child.ExecuteAsync(Stop, HookInput{})
			for range 2 {
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					t.Fatal("hooks did not start")
				}
			}
			first, second := child, parent
			if stopParent {
				first, second = parent, child
			}
			first.Shutdown()
			if id := <-ended; id != first.sessionID {
				t.Fatalf("shutdown canceled wrong owner: %s", id)
			}
			select {
			case id := <-ended:
				t.Fatalf("shutdown canceled other owner: %s", id)
			default:
			}
			second.Shutdown()
			if id := <-ended; id != second.sessionID {
				t.Fatalf("remaining hook owner: %s", id)
			}
		})
	}
}
