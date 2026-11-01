package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/reminder"
	"github.com/genai-io/san/internal/setting"
)

func TestCheckPromptStagesContextBeforeDecision(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		for _, extra := range []string{"", "hook context"} {
			t.Run(strings.Join([]string{map[bool]string{false: "accepted", true: "blocked"}[blocked], extra}, "/"), func(t *testing.T) {
				engine := hook.NewEngine(setting.NewData(), "session", t.TempDir(), "")
				calls := 0
				engine.AddSessionFunctionHook(hook.UserPromptSubmit, "", hook.FunctionHook{
					Callback: func(_ context.Context, input hook.HookInput) (hook.HookOutput, error) {
						calls++
						if input.Prompt != "raw prompt" {
							t.Fatalf("prompt = %q", input.Prompt)
						}
						allow := !blocked
						return hook.HookOutput{Continue: &allow, StopReason: "policy reason", HookSpecificOutput: &hook.HookSpecificOutput{AdditionalContext: extra}}, nil
					},
				})
				r := reminder.New()
				r.Enqueue("already pending")
				got, reason := CheckPrompt(t.Context(), "raw prompt", engine, r)
				if got != blocked || (blocked && reason != "policy reason") || calls != 1 {
					t.Fatalf("decision = %v, %q; calls = %d", got, reason, calls)
				}
				if r.Prepared() {
					t.Fatal("admission prepared conversation")
				}
				pending := strings.Join(r.Pending(), "\n")
				if !strings.Contains(pending, "already pending") || (extra != "" && !strings.Contains(pending, extra)) {
					t.Fatalf("pending = %q", pending)
				}
				// Blocked context deliberately leaks into the next delivered input.
				next := r.PrepareInput("later input")
				if !strings.Contains(next, "already pending") || (extra != "" && !strings.Contains(next, extra)) {
					t.Fatalf("later input = %q", next)
				}
				if again := r.PrepareInput("another"); again != "another" {
					t.Fatalf("context repeated: %q", again)
				}
			})
		}
	}
}

func TestCheckPromptHookBudgetIsNotPromptCancellation(t *testing.T) {
	engine := hook.NewEngine(setting.NewData(), "session", t.TempDir(), "")
	engine.AddSessionFunctionHook(hook.UserPromptSubmit, "", hook.FunctionHook{
		Callback: func(ctx context.Context, _ hook.HookInput) (hook.HookOutput, error) {
			<-ctx.Done()
			return hook.HookOutput{}, ctx.Err()
		},
	})
	promptCtx := t.Context()
	hookCtx, cancel := context.WithTimeout(promptCtx, time.Millisecond)
	defer cancel()
	r := reminder.New()
	if blocked, reason := CheckPrompt(hookCtx, "hello", engine, r); blocked || reason != "" {
		t.Fatalf("budget expiry blocked prompt: %v %q", blocked, reason)
	}
	if !errors.Is(hookCtx.Err(), context.DeadlineExceeded) || promptCtx.Err() != nil {
		t.Fatalf("hook error = %v; prompt error = %v", hookCtx.Err(), promptCtx.Err())
	}
	if r.Prepared() || !r.Empty() {
		t.Fatal("failed hook changed reminder state")
	}
}
