package app

import (
	"context"
	"errors"
	"testing"

	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestHookRecordingMissingModel(t *testing.T) {
	for _, kind := range []string{"prompt", "agent"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			store, err := session.OpenStore(t.Context(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			owner, err := store.Create(t.Context(), session.CreateOptions{ID: "missing-model"})
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			s := setting.NewData()
			s.Hooks["PreToolUse"] = []setting.Hook{{Hooks: []setting.HookCmd{{Type: kind, Prompt: "verify"}}}}
			e := hook.NewEngine(s, owner.ID(), t.TempDir(), "")
			defer e.Shutdown()
			e.SetRecordedLLMCompleter(func(context.Context, string, string, string) (hook.LLMCompletion, error) {
				t.Fatal("missing-model hook invoked completer")
				return hook.LLMCompletion{}, nil
			}, "")
			e.SetCompletionRecorder(func(ctx context.Context, a hook.HookFiredAudit) error {
				return owner.Completion(session.PurposeHook).FinishHook(ctx, a)
			})
			e.Execute(t.Context(), hook.PreToolUse, hook.HookInput{})
			p := owner.Snapshot()
			events := p.Events()
			if len(events) != 2 || events[1].Type != session.EventHookFinish || events[1].Source != nil ||
				events[1].Payload.(*session.HookFinishPayload).Outcome != session.HookError {
				t.Fatalf("missing terminal error or invented inference: %+v", events)
			}
		})
	}
}

func TestStopHookRecordingErrorAndStaleOwner(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(map[bool]string{false: "current", true: "stale"}[stale], func(t *testing.T) {
			m := newRecapModel(t)
			m.pendingStopHook = &pendingStopHook{session: m.services.Session, outbox: m.AgentOutbox()}
			before := len(m.conv.Messages)
			if stale {
				m.services.Session = new(session.Session)
			}
			m.handleStopHookResult(stopHookResultMsg{pending: m.pendingStopHook, Blocked: true, Reason: "must not continue", Err: &hook.RecordingError{Err: errors.New("storage")}})
			want := before + 1
			if stale {
				want = before
			}
			if len(m.conv.Messages) != want || m.pendingStopHook != nil {
				t.Fatal("Stop result affected wrong owner or admitted continuation")
			}
		})
	}
}

func TestHookRecordingNativeCompletion(t *testing.T) {
	for _, kind := range []string{"prompt", "agent"} {
		for _, output := range []string{`{"continue":false,"stopReason":"no"}`, `invalid`} {
			t.Run(kind+output, func(t *testing.T) {
				t.Setenv("HOME", t.TempDir())
				ctx := context.Background()
				store, err := session.OpenStore(ctx, t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				owner, err := store.Create(ctx, session.CreateOptions{ID: "hook-test"})
				if err != nil {
					t.Fatal(err)
				}
				defer owner.Close()
				provider := &compactHTTPProvider{client: ai.NewClientWithDriver(aitest.New(aitest.Says(output)), ai.Model{ID: "m"})}
				settings := setting.NewData()
				settings.Hooks["PreToolUse"] = []setting.Hook{{Hooks: []setting.HookCmd{{Type: kind, Prompt: "verify"}}}}
				engine := hook.NewEngine(settings, "hook-test", t.TempDir(), "")
				engine.SetRecordedLLMCompleter(buildRecordedHookCompleter(provider, owner), "m")
				engine.Execute(ctx, hook.PreToolUse, hook.HookInput{})
				starts, finishes, hooks := 0, 0, 0
				snapshot := owner.Snapshot()
				for _, event := range snapshot.Events() {
					switch payload := event.Payload.(type) {
					case *session.InferenceStartPayload:
						starts++
						if payload.Purpose != session.PurposeHook {
							t.Fatalf("purpose %s", payload.Purpose)
						}
						request, err := owner.InferenceRequest(ctx, event.Seq)
						if err != nil {
							t.Fatal(err)
						}
						if len(request.Messages) != 1 || request.System == "" {
							t.Fatalf("request: %+v", request)
						}
					case *session.InferenceFinishPayload:
						finishes++
						if payload.Error != nil {
							t.Fatalf("successful inference marked failed: %+v", payload)
						}
					case *session.HookFinishPayload:
						hooks++
						want := "blocked"
						if output == "invalid" {
							want = "error"
						}
						if payload.Outcome != want {
							t.Fatalf("hook outcome %s", payload.Outcome)
						}
					}
				}
				if starts != 1 || finishes != 1 || hooks != 1 {
					t.Fatalf("starts=%d finishes=%d hooks=%d", starts, finishes, hooks)
				}
			})
		}
	}
}
