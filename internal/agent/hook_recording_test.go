package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/reminder"
	"github.com/genai-io/san/internal/setting"
)

func TestHookRecordingFailureStopsPromptAndStop(t *testing.T) {
	for _, event := range []hook.EventType{hook.UserPromptSubmit, hook.Stop} {
		t.Run(string(event), func(t *testing.T) {
			e := hook.NewEngine(setting.NewData(), "original", t.TempDir(), "")
			defer e.Shutdown()
			e.AddRuntimeFunctionHook(event, "", hook.FunctionHook{Callback: func(context.Context, hook.HookInput) (hook.HookOutput, error) {
				return hook.HookOutput{SystemMessage: "not committed", Continue: new(false), StopReason: "continue work"}, nil
			}})
			boom := errors.New("storage failed")
			e.SetCompletionRecorder(func(context.Context, hook.HookFiredAudit) error { return boom })
			if event == hook.UserPromptSubmit {
				r := reminder.New()
				blocked, reason := CheckPrompt(t.Context(), "go", e, r)
				if !blocked || reason == "" || len(r.Pending()) != 0 {
					t.Fatalf("prompt admitted uncommitted hook: blocked=%v reason=%s reminders=%v", blocked, reason, r.Pending())
				}
				return
			}
			decision := NewStopCheck(e, "answer").Run(t.Context())
			called := false
			continuation, err := decision.Finish(func() error { called = true; return nil })
			var recording *hook.RecordingError
			if !errors.Is(err, boom) || !errors.As(err, &recording) || called || continuation != "" {
				t.Fatalf("Stop recording failed open: continuation=%q err=%v state=%v", continuation, err, called)
			}
		})
	}
}

func TestStopCheckCapturesInvocationBeforeQueuing(t *testing.T) {
	s := setting.NewData()
	s.Hooks["Stop"] = []setting.Hook{{Hooks: []setting.HookCmd{{Type: "prompt", Prompt: "original prompt"}}}}
	e := hook.NewEngine(s, "original", "/original", "original-transcript")
	defer e.Shutdown()
	original, replacement := 0, 0
	e.SetRecordedLLMCompleter(func(_ context.Context, _, user, model string) (hook.LLMCompletion, error) {
		original++
		if model != "original-model" || !strings.Contains(user, `"session_id":"original"`) || !strings.Contains(user, "original prompt") {
			t.Errorf("invocation retargeted: model=%s input=%s", model, user)
		}
		return hook.LLMCompletion{Text: `{}`, Finish: func(context.Context, hook.HookFiredAudit) error { return nil }}, nil
	}, "original-model")
	check := NewStopCheck(e, "answer")
	e.SetSession("replacement", "replacement-transcript")
	e.SetSettings(setting.NewData())
	e.SetRecordedLLMCompleter(func(context.Context, string, string, string) (hook.LLMCompletion, error) {
		replacement++
		return hook.LLMCompletion{}, nil
	}, "replacement-model")
	if result := check.Run(t.Context()); result.Err != nil || original != 1 || replacement != 0 {
		t.Fatalf("result=%+v original=%d replacement=%d", result, original, replacement)
	}
}
