package agent

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/setting"
)

func TestEndTurnOrdering(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		queued, autopilot, failed bool
		want                      []string
	}{
		{name: "idle", want: []string{"record", "queue", "autopilot", "idle"}},
		{name: "queued", queued: true, autopilot: true, want: []string{"record", "queue"}},
		{name: "autopilot", autopilot: true, want: []string{"record", "queue", "autopilot"}},
		{name: "record-failed", failed: true, want: []string{"record"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			failure := errors.New("record failed")
			err := EndTurn(TurnCallbacks{
				RecordState: func() error {
					calls = append(calls, "record")
					if tc.failed {
						return failure
					}
					return nil
				},
				NextInput: func() bool { calls = append(calls, "queue"); return tc.queued },
				AutoPilot: func() bool { calls = append(calls, "autopilot"); return tc.autopilot },
				Idle:      func() { calls = append(calls, "idle") },
			})
			if !reflect.DeepEqual(calls, tc.want) || errors.Is(err, failure) != tc.failed {
				t.Fatalf("calls=%v err=%v", calls, err)
			}
		})
	}
}

func TestStopDecisionRecording(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		for _, failed := range []bool{false, true} {
			d := StopDecision{Blocked: blocked, Reason: "more work"}
			count := 0
			failure := errors.New("record failed")
			continuation, err := d.Finish(func() error {
				count++
				if failed {
					return failure
				}
				return nil
			})
			want := ""
			if blocked && !failed {
				want = "Stop hook blocked: more work"
			}
			if continuation != want || count != 1 || errors.Is(err, failure) != failed {
				t.Fatalf("blocked=%v failed=%v: continuation=%q count=%d err=%v", blocked, failed, continuation, count, err)
			}
		}
	}
}

func TestStopCheckEnginePolicy(t *testing.T) {
	for _, mode := range []string{"allow", "block", "error", "nil"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "nil" {
				if got := NewStopCheck(nil, "answer").Run(t.Context()); got != (StopDecision{}) {
					t.Fatal(got)
				}
				return
			}
			engine := hook.NewEngine(setting.NewData(), "session", t.TempDir(), "")
			called := false
			engine.AddSessionFunctionHook(hook.Stop, "", hook.FunctionHook{Callback: func(_ context.Context, input hook.HookInput) (hook.HookOutput, error) {
				called = true
				if input.LastAssistantMessage != "answer" || input.StopHookActive == nil || !*input.StopHookActive {
					t.Fatalf("input=%+v", input)
				}
				if mode == "error" {
					return hook.HookOutput{}, errors.New("hook failed")
				}
				allow := mode != "block"
				return hook.HookOutput{Continue: &allow, StopReason: "more work"}, nil
			}})
			got := NewStopCheck(engine, "answer").Run(t.Context())
			if !called || got.Blocked != (mode == "block") || got.Blocked && got.Reason != "more work" {
				t.Fatalf("decision=%+v called=%v", got, called)
			}
		})
	}
}
