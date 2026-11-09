package session

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/transcript"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

// Exercise the producer observer without a TUI: execution completion must not
// stand in for consumer cleanup, and successful cleanup cannot hide write errors.
func TestEventRecorderTurnFinalization(t *testing.T) {
	for _, mode := range []string{"saved", "essential-write-failed", "closed", "audit-write-failed"} {
		t.Run(mode, func(t *testing.T) {
			fs, err := transcript.NewStore(t.TempDir(), "project")
			if err != nil {
				t.Fatal(err)
			}
			rec := mustSession(t, fs, LegacyCreateOptions{ID: "session"})
			switch mode {
			case "essential-write-failed":
				rec.RecordEvent(sdkagent.MessageAdded{Message: ai.UserMessage("missing ID")})
			case "closed":
				if err := rec.Close(); err != nil {
					t.Fatal(err)
				}
			case "audit-write-failed":
				if err := rec.Append(context.Background(), transcript.Record{Type: transcript.HookFired}); err == nil {
					t.Fatal("malformed audit unexpectedly accepted")
				}
			}
			wantErr := rec.Err()
			if (wantErr != nil) != (mode == "essential-write-failed" || mode == "closed") {
				t.Fatalf("unexpected sticky error: %v", wantErr)
			}
			client := aitest.Always(aitest.Says("done")).Client()
			ag := core.NewAgent(core.Config{
				Client: func([]core.Message) (*ai.Client, error) { return client, nil },
				System: core.NewSystem(), Tools: core.NewTools(), OnEvent: rec.RecordEvent,
			})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			done := make(chan error, 1)
			go func() { done <- ag.Run(ctx) }()
			defer func() { cancel(); <-done }()
			ag.Append(core.UserMessage("work", nil))
			for {
				select {
				case event := <-ag.Outbox():
					end, ok := event.(core.TurnEnded)
					if !ok {
						continue
					}
					receipt := end.Result.Finalization
					if receipt == nil {
						t.Fatal("missing receipt")
					}
					select {
					case <-receipt.ExecutionDone():
					default:
						t.Fatal("terminal delivered before execution completed")
					}
					short, stop := context.WithTimeout(ctx, 10*time.Millisecond)
					waitErr := receipt.Wait(short)
					stop()
					if !errors.Is(waitErr, context.DeadlineExceeded) {
						t.Fatalf("recorder acknowledged consumer cleanup: %v", waitErr)
					}
					receipt.Acknowledge(nil)
					if err := receipt.Wait(ctx); !errors.Is(err, wantErr) {
						t.Fatalf("receipt error = %v, want %v", err, wantErr)
					}
					return
				case <-ctx.Done():
					t.Fatal("missing terminal event")
				}
			}
		})
	}
}

func TestExecutionAuditCallbacksKeepOpenedIdentityAndOrder(t *testing.T) {
	dir := t.TempDir()
	fs, err := transcript.NewStore(dir, "project")
	if err != nil {
		t.Fatal(err)
	}
	opened := mustSession(t, fs, LegacyCreateOptions{ID: "original"})
	recordHook, recordSkill := opened.RecordHookAudit, opened.RecordSkillStateChange
	// Replacing the application's session variable cannot retarget callbacks.
	opened = mustSession(t, fs, LegacyCreateOptions{ID: "replacement"})
	audit := hook.HookFiredAudit{Event: "PreToolUse", Source: "project", Matcher: "Bash", Outcome: "allow", Reason: "checked", Duration: 12 * time.Millisecond}
	recordHook(audit)
	recordSkill("review", "inactive", "active", "main")
	var got []transcript.Record
	for _, r := range readAllRecords(t, dir, "original") {
		if r.Type == transcript.HookFired || r.Type == transcript.SkillStateChanged {
			got = append(got, r)
		}
	}
	if len(got) != 2 {
		t.Fatalf("audit records = %d, want 2", len(got))
	}
	wantHook := &transcript.HookRecord{Event: audit.Event, Source: audit.Source, Matcher: audit.Matcher, Outcome: audit.Outcome, Reason: audit.Reason, LatencyMs: 12}
	wantSkill := &transcript.SkillRecord{Name: "review", Previous: "inactive", Current: "active", Caller: "main"}
	if !reflect.DeepEqual(got[0].Hook, wantHook) || !reflect.DeepEqual(got[1].Skill, wantSkill) {
		t.Fatalf("audit payload or order changed: %+v", got)
	}
	for _, r := range readAllRecords(t, dir, opened.ID()) {
		if r.Hook != nil || r.Skill != nil {
			t.Fatal("callbacks retargeted replacement session")
		}
	}
}
