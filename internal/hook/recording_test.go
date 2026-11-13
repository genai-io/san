package hook

import (
	"context"
	"errors"
	"testing"

	"github.com/genai-io/san/internal/setting"
)

func TestRecordedCompletionInterpretedBeforeFinish(t *testing.T) {
	for _, kind := range []string{"prompt", "agent"} {
		for _, text := range []string{`{"continue":false,"stopReason":"stop"}`, `invalid`} {
			t.Run(kind+text, func(t *testing.T) {
				s := setting.NewData()
				s.Hooks["PreToolUse"] = []setting.Hook{{Hooks: []setting.HookCmd{{Type: kind, Prompt: "check"}}}}
				e := NewEngine(s, "s", t.TempDir(), "")
				var got HookFiredAudit
				e.SetRecordedLLMCompleter(func(context.Context, string, string, string) (LLMCompletion, error) {
					return LLMCompletion{Text: text, Finish: func(ctx context.Context, a HookFiredAudit) error { got = a; return nil }}, nil
				}, "model")
				e.Execute(context.Background(), PreToolUse, HookInput{})
				want := "blocked"
				if text == "invalid" {
					want = "error"
				}
				if got.Outcome != want || got.Event != "PreToolUse" || got.Duration <= 0 {
					t.Fatalf("audit: %+v", got)
				}
			})
		}
	}
}

func TestRecordedCompletionRequiresFinisher(t *testing.T) {
	s := setting.NewData()
	s.Hooks["PreToolUse"] = []setting.Hook{{Hooks: []setting.HookCmd{{Type: "prompt", Prompt: "check"}}}}
	e := NewEngine(s, "s", t.TempDir(), "")
	defer e.Shutdown()
	e.SetRecordedLLMCompleter(func(context.Context, string, string, string) (LLMCompletion, error) {
		return LLMCompletion{Text: `{}`}, nil
	}, "model")
	r := e.Execute(t.Context(), PreToolUse, HookInput{})
	var recording *RecordingError
	if r.ShouldContinue || !errors.As(r.Error, &recording) {
		t.Fatalf("missing finisher failed open: %+v", r)
	}
}

func TestRecordedCompletionFailureClosed(t *testing.T) {
	s := setting.NewData()
	s.Hooks["PreToolUse"] = []setting.Hook{{Hooks: []setting.HookCmd{{Type: "prompt", Prompt: "check"}}}}
	e := NewEngine(s, "s", t.TempDir(), "")
	e.SetRecordedLLMCompleter(func(context.Context, string, string, string) (LLMCompletion, error) {
		return LLMCompletion{Text: `{}`, Finish: func(context.Context, HookFiredAudit) error { return errors.New("storage") }}, nil
	}, "model")
	r := e.Execute(context.Background(), PreToolUse, HookInput{})
	var recording *RecordingError
	if r.ShouldContinue || !errors.As(r.Error, &recording) {
		t.Fatalf("not closed: %+v", r)
	}
}

func TestCompletionRecorderCapturedBeforeExecution(t *testing.T) {
	e := NewEngine(setting.NewData(), "s", t.TempDir(), "")
	original, replacement := 0, 0
	e.SetCompletionRecorder(func(context.Context, HookFiredAudit) error { original++; return nil })
	e.AddRuntimeFunctionHook(PreToolUse, "", FunctionHook{Callback: func(context.Context, HookInput) (HookOutput, error) {
		e.SetCompletionRecorder(func(context.Context, HookFiredAudit) error { replacement++; return nil })
		return HookOutput{}, nil
	}})
	e.Execute(context.Background(), PreToolUse, HookInput{})
	if original != 1 || replacement != 0 {
		t.Fatalf("original=%d replacement=%d", original, replacement)
	}
}
