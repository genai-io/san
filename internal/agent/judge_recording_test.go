package agent

import (
	"context"
	"errors"
	"iter"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/judge"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

type judgeRecordingTool struct{ runs *atomic.Int32 }

func (judgeRecordingTool) Schema() core.ToolSchema { return core.ToolSchema{Name: "Bash"} }
func (v judgeRecordingTool) Run(context.Context, ai.ToolCall) (sdkagent.Result, error) {
	v.runs.Add(1)
	return sdkagent.TextResult("done"), nil
}

// Exercise the real judge completion, gate, recorder and SDK dispatch together.
func TestJudgeRecordingDispatch(t *testing.T) {
	for _, mode := range []string{"allow", "escalate", "invalid", "provider-error", "admission-failure", "finish-failure", "escalate-finish-failure", "late-allow"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			store, err := session.OpenStore(ctx, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			owner, err := store.Create(ctx, session.CreateOptions{ID: "judge-recording"})
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			permissions := &setting.SessionPermissions{Mode: setting.ModeAutoPilot}
			policy := PermissionPolicy{Settings: setting.NewData(), Session: permissions}
			var prepared *ai.Request
			review := aitest.New(func(ctx context.Context, r *ai.Request) iter.Seq2[ai.Delta, error] {
				prepared = r
				text := `{"decision":"allow","reason":"routine"}`
				var callErr error
				switch mode {
				case "escalate":
					text = `{"decision":"escalate","reason":"ask human"}`
				case "invalid":
					text = "not a verdict"
				case "provider-error":
					callErr = errors.New("provider offline")
				case "finish-failure", "escalate-finish-failure":
					if mode == "escalate-finish-failure" {
						text = `{"decision":"escalate","reason":"ask human"}`
					}
					if err := owner.Close(); err != nil {
						t.Error(err)
					}
				case "late-allow":
					cancel()
				}
				return aitest.Replies(ai.Response{Content: ai.TextContent(text), Usage: ai.Usage{Input: 17, Output: 4}, Err: callErr})(ctx, r)
			})
			reviewer := judge.New(runOnceProvider{review}, "fake")
			gate := NewPermissionGate(policy.Decide)
			defer gate.Close()
			var positives atomic.Int32
			gate.SetJudge(func(ctx context.Context, name string, args map[string]any, reason string) PermJudgeResult {
				if mode == "admission-failure" {
					if err := owner.Close(); err != nil {
						t.Error(err)
					}
				}
				return policy.Judge(ctx, name, args, reason, JudgeSnapshot{Engaged: true, Enabled: true, CWD: "/workspace", UnderGit: true, Mission: "verify tests", Judge: reviewer.Permission, Recording: owner, Observe: func(_ context.Context, allow bool, _ string) {
					if allow {
						positives.Add(1)
					}
				}})
			})
			rec := session.NewRecorder(owner, "fake")
			main := aitest.New(aitest.Asks(ai.ToolCall{ID: "call-judge", Name: "Bash", Input: `{"command":"go test ./..."}`}), aitest.Says("done"))
			var runs atomic.Int32
			ag := core.NewAgent(core.Config{System: core.NewSystem(), Tools: core.NewTools(judgeRecordingTool{&runs}), Client: func([]core.Message) (*ai.Client, error) { return main.Client(), nil }, Gate: tool.HookedPermission(nil, gate, rec.RecordPermission), InferenceAdmission: rec.InferenceAdmission, ToolAdmission: rec.ToolAdmission, OutputAdmission: rec.OutputAdmission, OnEvent: rec.RecordEvent, OutboxBuf: -1, MaxSteps: 3})
			ag.Append(core.UserMessage("verify", nil))
			done := make(chan error, 1)
			go func() { _, err := ag.ThinkAct(ctx); done <- err }()
			fallback := mode == "escalate" || mode == "invalid" || mode == "provider-error"
			if fallback {
				req, ok := gate.WaitRequest(ctx)
				if !ok {
					t.Fatal("missing human escalation")
				}
				// No judge denial or premature grant may exist while the user decides.
				snapshot := owner.Snapshot()
				for _, event := range snapshot.Events() {
					if p, ok := event.Payload.(*session.PermissionDecidePayload); ok {
						t.Fatalf("premature decision: %+v", p)
					}
				}
				req.Response <- PermGateResponse{Allow: true, By: "user"}
			}
			select {
			case err = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("dispatch did not complete")
			}
			fatal := mode == "admission-failure" || mode == "finish-failure" || mode == "escalate-finish-failure" || mode == "late-allow"
			if fatal {
				if err == nil || runs.Load() != 0 || main.Calls() != 1 || positives.Load() != 0 || len(permissions.Snapshot().AllowedPatterns) != 0 {
					t.Fatalf("failed open: err=%v runs=%d calls=%d positive=%d grants=%v", err, runs.Load(), main.Calls(), positives.Load(), permissions.Snapshot().AllowedPatterns)
				}
				if mode != "late-allow" && !errors.Is(err, session.ErrClosed) {
					t.Fatalf("lost storage error: %v", err)
				}
				if mode == "admission-failure" && review.Calls() != 0 {
					t.Fatal("provider dispatched before admission")
				}
				select {
				case <-gate.requests:
					t.Fatal("accounting failure prompted the user")
				default:
				}
				if mode == "late-allow" {
					snapshot := owner.Snapshot()
					if len(snapshot.Unfinished().Inferences) != 0 {
						t.Fatal("cancelled judge left unfinished accounting")
					}
				}
				return
			}
			if err != nil || runs.Load() != 1 || main.Calls() != 2 {
				t.Fatalf("err=%v runs=%d calls=%d", err, runs.Load(), main.Calls())
			}
			var starts, finishes, decisions int
			var judgeSource uint64
			snapshot := owner.Snapshot()
			for _, event := range snapshot.Events() {
				switch p := event.Payload.(type) {
				case *session.InferenceStartPayload:
					if p.Purpose != session.PurposeJudge {
						continue
					}
					starts++
					judgeSource = event.Seq
					if p.Request.Provider == nil || *p.Request.Provider != (runOnceProvider{}).Name() || p.Request.Model == nil || *p.Request.Model != review.Client().Model().ID {
						t.Fatalf("wrong resolved judge: %+v", p.Request)
					}
					if p.Input.Standalone == "" {
						t.Fatal("judge request not standalone")
					}
					rebuilt, e := owner.InferenceRequest(ctx, event.Seq)
					if e != nil {
						t.Fatal(e)
					}
					if prepared == nil || !reflect.DeepEqual(rebuilt, prepared) || len(rebuilt.Tools) != 0 {
						t.Fatal("prepared judge request did not reconstruct exactly")
					}
				case *session.InferenceFinishPayload:
					if p.Usage != nil && p.Usage.Input == 17 {
						finishes++
						wantOutcome := session.OutcomeSuccess
						if mode == "provider-error" {
							wantOutcome = session.OutcomeFailure
						}
						if p.Usage.Output != 4 || p.Outcome != wantOutcome {
							t.Fatalf("lost judge outcome: %+v", p)
						}
					}
				case *session.PermissionDecidePayload:
					decisions++
					want := "judge"
					if fallback {
						want = "user"
					}
					if fallback && event.Source != nil || !fallback && (event.Source == nil || event.Source.Seq != judgeSource) {
						t.Fatalf("misattributed final decision: %+v", event.Source)
					}
					if p.By != want || p.Decision != "allow" || p.CallID != "call-judge" {
						t.Fatalf("wrong final authority: %+v", p)
					}
				}
			}
			if starts != 1 || finishes != 1 || decisions != 1 || len(snapshot.Unfinished().Inferences) != 0 {
				t.Fatalf("starts=%d finishes=%d decisions=%d", starts, finishes, decisions)
			}
			if (positives.Load() == 1) != (!fallback) || (len(permissions.Snapshot().AllowedPatterns) > 0) != (!fallback) {
				t.Fatal("judge grant/observation does not match committed authority")
			}
		})
	}
}
