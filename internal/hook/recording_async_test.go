package hook

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/genai-io/san/internal/setting"
)

func TestDetachedHookKeepsWholeInvocation(t *testing.T) {
	s := setting.NewData()
	s.Hooks["PreToolUse"] = []setting.Hook{{Hooks: []setting.HookCmd{
		{Type: "prompt", Prompt: "$ARGUMENTS"}, {Type: "agent", Prompt: "$ARGUMENTS", Async: true},
	}}}
	e := NewEngine(s, "original", "/original", "original-transcript")
	defer e.Shutdown()
	e.SetPermissionMode("plan")
	var calls, finishes, replacement atomic.Int32
	e.SetRecordedLLMCompleter(func(ctx context.Context, _, user, model string) (LLMCompletion, error) {
		var input HookInput
		if err := json.Unmarshal([]byte(user), &input); err != nil {
			t.Error(err)
		}
		if model != "original-model" || input.SessionID != "original" || input.Cwd != "/original" || input.TranscriptPath != "original-transcript" || input.PermissionMode != "plan" || ctx.Err() != nil {
			t.Errorf("retargeted invocation: model=%s input=%+v err=%v", model, input, ctx.Err())
		}
		if calls.Add(1) == 1 {
			e.SetSession("replacement", "replacement-transcript")
			e.SetCwd("/replacement")
			e.SetPermissionMode("normal")
			e.SetSettings(setting.NewData())
			e.SetRecordedLLMCompleter(func(context.Context, string, string, string) (LLMCompletion, error) {
				replacement.Add(1)
				return LLMCompletion{}, nil
			}, "replacement-model")
		}
		return LLMCompletion{Text: `{}`, Finish: func(context.Context, HookFiredAudit) error { finishes.Add(1); return nil }}, nil
	}, "original-model")
	e.Execute(t.Context(), PreToolUse, HookInput{})
	e.detachedWg.Wait()
	if calls.Load() != 2 || finishes.Load() != 2 || replacement.Load() != 0 {
		t.Fatalf("calls=%d finishes=%d replacement=%d", calls.Load(), finishes.Load(), replacement.Load())
	}
}

func TestRecordedAsyncCapturesCompleterAtLaunch(t *testing.T) {
	settings := setting.NewData()
	settings.Hooks["PreToolUse"] = []setting.Hook{{Hooks: []setting.HookCmd{{Type: "prompt", Prompt: "check", Async: true}}}}
	e := NewEngine(settings, "s", t.TempDir(), "")
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan HookFiredAudit, 1)
	var replacement atomic.Int32
	e.SetRecordedLLMCompleter(func(context.Context, string, string, string) (LLMCompletion, error) {
		close(entered)
		<-release
		return LLMCompletion{Text: `{}`, Finish: func(_ context.Context, a HookFiredAudit) error { finished <- a; return nil }}, nil
	}, "model")
	ctx, cancel := context.WithCancel(context.Background())
	e.Execute(ctx, PreToolUse, HookInput{})
	cancel()
	<-entered
	select {
	case <-finished:
		t.Fatal("recorded launch as completion")
	default:
	}
	e.SetRecordedLLMCompleter(func(context.Context, string, string, string) (LLMCompletion, error) {
		replacement.Add(1)
		return LLMCompletion{}, nil
	}, "model")
	close(release)
	e.detachedWg.Wait()
	a := <-finished
	if a.Outcome != "ran" || replacement.Load() != 0 {
		t.Fatalf("audit=%+v replacement=%d", a, replacement.Load())
	}
	e.Shutdown()
}
