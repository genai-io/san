package tool

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"

	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool/perm"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

type resolvedChecker struct {
	fakeHookAwareChecker
	result perm.Resolution
	input  map[string]any
}

func (c *resolvedChecker) Resolve(_ context.Context, _ string, input map[string]any, force bool, reason string) perm.Resolution {
	c.called, c.forcePrompt, c.reason, c.input = true, force, reason, input
	return c.result
}

func TestFinalPermissionRecording(t *testing.T) {
	for _, tc := range []struct {
		name          string
		outcome       hook.HookOutcome
		by            string
		block, called bool
	}{
		{"static", hook.HookOutcome{}, "config", false, true},
		{"hook allow", hook.HookOutcome{PermissionAllow: true}, "hook", false, false},
		{"hook deny", hook.HookOutcome{ShouldBlock: true}, "hook", true, false},
		{"forced ask", hook.HookOutcome{ForceAsk: true, UpdatedInput: map[string]any{"command": "rewritten"}}, "user", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checker := &resolvedChecker{result: perm.Resolution{Decision: "allow", By: tc.by}}
			var records []perm.Resolution
			g := HookedPermission(&fakeHookHandler{outcome: tc.outcome}, checker, func(_ context.Context, r perm.Resolution) error { records = append(records, r); return nil })
			d, err := g(context.Background(), sdkagent.PreToolContext{Call: ai.ToolCall{ID: "call-1", Name: "Bash", Input: `{"command":"original"}`}})
			if err != nil || d.Block != tc.block || checker.called != tc.called {
				t.Fatalf("decision=%+v err=%v checker=%+v", d, err, checker)
			}
			if len(records) != 1 || records[0].By != tc.by || records[0].CallID != "call-1" {
				t.Fatalf("records=%+v", records)
			}
			if tc.outcome.ForceAsk && (!checker.forcePrompt || checker.input["command"] != "rewritten") {
				t.Fatalf("forced prompt lost rewritten input: %+v", checker)
			}
		})
	}
}

type recordingDispatchTool struct{ runs *atomic.Int32 }

func (recordingDispatchTool) Schema() core.ToolSchema { return core.ToolSchema{Name: "Bash"} }
func (t recordingDispatchTool) Run(context.Context, ai.ToolCall) (sdkagent.Result, error) {
	t.runs.Add(1)
	return sdkagent.TextResult("done"), nil
}

func TestRecorderFailureStopsRealSDKDispatch(t *testing.T) {
	var runs atomic.Int32
	driver := aitest.New(aitest.Asks(ai.ToolCall{ID: "call-1", Name: "Bash", Input: `{}`}), aitest.Says("must not dispatch"))
	client := ai.NewClientWithDriver(driver, ai.Model{ID: "stub", API: "stub", ContextWindow: 200000})
	boom := errors.New("recorder unavailable")
	checker := &resolvedChecker{result: perm.Resolution{Decision: "allow", By: "config"}}
	var records atomic.Int32
	ag := core.NewAgent(core.Config{
		System: core.NewSystem(), OutboxBuf: -1, MaxSteps: 3,
		Client: func([]core.Message) (*ai.Client, error) { return client, nil },
		Tools:  core.NewTools(recordingDispatchTool{runs: &runs}),
		Gate: HookedPermission(nil, checker, func(_ context.Context, r perm.Resolution) error {
			records.Add(1)
			if r.CallID != "call-1" {
				t.Errorf("call ID=%q", r.CallID)
			}
			return boom
		}),
	})
	ag.Append(core.UserMessage("go", nil))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := ag.ThinkAct(ctx)
	if !errors.Is(err, boom) || runs.Load() != 0 || records.Load() != 1 {
		t.Fatalf("err=%v runs=%d records=%d", err, runs.Load(), records.Load())
	}
	if driver.Calls() != 1 {
		t.Fatalf("subsequent model dispatch: %d", driver.Calls())
	}
}

func TestHookRecordingFailureStopsRealSDKDispatch(t *testing.T) {
	var runs atomic.Int32
	driver := aitest.New(aitest.Asks(ai.ToolCall{ID: "call-1", Name: "Bash", Input: `{}`}), aitest.Says("must not dispatch"))
	client := ai.NewClientWithDriver(driver, ai.Model{ID: "stub"})
	boom := errors.New("hook storage unavailable")
	engine := hook.NewEngine(setting.NewData(), "s", t.TempDir(), "")
	defer engine.Shutdown()
	engine.AddRuntimeFunctionHook(hook.PreToolUse, "", hook.FunctionHook{Callback: func(context.Context, hook.HookInput) (hook.HookOutput, error) {
		return hook.HookOutput{HookSpecificOutput: &hook.HookSpecificOutput{PermissionDecision: "allow"}}, nil
	}})
	engine.SetCompletionRecorder(func(context.Context, hook.HookFiredAudit) error { return boom })
	checker := &resolvedChecker{result: perm.Resolution{Decision: "allow", By: "config"}}
	ag := core.NewAgent(core.Config{
		System: core.NewSystem(), OutboxBuf: -1, MaxSteps: 3,
		Client: func([]core.Message) (*ai.Client, error) { return client, nil },
		Tools:  core.NewTools(recordingDispatchTool{runs: &runs}), Gate: HookedPermission(engine, checker),
	})
	ag.Append(core.UserMessage("go", nil))
	_, err := ag.ThinkAct(t.Context())
	var recording *hook.RecordingError
	if !errors.As(err, &recording) || !errors.Is(err, boom) || runs.Load() != 0 || checker.called || driver.Calls() != 1 {
		t.Fatalf("hook durability failed open: err=%v runs=%d checks=%v dispatches=%d", err, runs.Load(), checker.called, driver.Calls())
	}
}

func TestCancelledPermissionRecordsWithLiveContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	checker := &resolvedChecker{result: perm.Resolution{Decision: "cancelled", By: "user"}}
	recorded := false
	gate := HookedPermission(nil, checker, func(ctx context.Context, r perm.Resolution) error {
		recorded = true
		if ctx.Err() != nil || r.Decision != "cancelled" {
			t.Errorf("lost terminal resolution: %+v err=%v", r, ctx.Err())
		}
		return nil
	})
	d, err := gate(ctx, sdkagent.PreToolContext{Call: ai.ToolCall{ID: "call", Name: "Bash", Input: `{}`}})
	if err != nil || !d.Block || !recorded {
		t.Fatalf("decision=%+v err=%v recorded=%v", d, err, recorded)
	}
}

func TestJudgeCompletionUsesFinalPermission(t *testing.T) {
	for _, mode := range []string{"allow", "rewrite-denied", "cancelled", "fatal", "without-recorder"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			boom := errors.New("judge accounting failed")
			finishes, records := 0, 0
			checker := &resolvedChecker{result: perm.Resolution{Decision: "allow", By: "judge"}}
			checker.result.Finish = func(ctx context.Context, r perm.Resolution) error {
				finishes++
				wantDecision, wantBy := "allow", "judge"
				if mode == "rewrite-denied" {
					wantDecision, wantBy = "deny", "hook"
				} else if mode == "cancelled" {
					wantDecision = "cancelled"
				}
				if ctx.Err() != nil || r.CallID != "call" || r.Decision != wantDecision || r.By != wantBy {
					t.Errorf("final resolution=%+v context error=%v", r, ctx.Err())
				}
				return nil
			}
			var hooks hook.Handler
			if mode == "rewrite-denied" {
				hooks = &fakeHookHandler{outcome: hook.HookOutcome{UpdatedInput: map[string]any{"invalid": make(chan int)}}}
			}
			if mode == "cancelled" {
				cancel()
			}
			if mode == "fatal" {
				checker.result = perm.Resolution{Err: boom}
			}
			var record []perm.RecordDecision
			if mode != "without-recorder" {
				record = append(record, func(ctx context.Context, r perm.Resolution) error {
					records++
					return r.Finish(ctx, r)
				})
			}
			d, err := HookedPermission(hooks, checker, record...)(ctx, sdkagent.PreToolContext{
				Call: ai.ToolCall{ID: "call", Name: "Bash", Input: `{}`},
			})
			if mode == "fatal" {
				if !errors.Is(err, boom) || finishes != 0 || records != 0 {
					t.Fatalf("err=%v finishes=%d records=%d", err, finishes, records)
				}
				return
			}
			if err != nil || finishes != 1 || d.Block != (mode == "rewrite-denied" || mode == "cancelled") {
				t.Fatalf("decision=%+v err=%v finishes=%d", d, err, finishes)
			}
			if mode == "without-recorder" && records != 0 {
				t.Fatal("unexpected recorder")
			}
		})
	}
}

func TestPermissionRecordingFailsClosed(t *testing.T) {
	boom := errors.New("record failed")
	recorder := func(context.Context, perm.Resolution) error { return boom }
	checker := &resolvedChecker{result: perm.Resolution{Decision: "allow", By: "mode"}}
	c := sdkagent.PreToolContext{Call: ai.ToolCall{Name: "Bash", Input: `{}`}}
	if _, err := HookedPermission(nil, checker, recorder)(context.Background(), c); !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
	if _, err := HookedPermission(nil, &fakeHookAwareChecker{}, recorder)(context.Background(), c); err == nil {
		t.Fatal("missing resolver accepted")
	}
	if _, err := HookedPermission(nil, checker, recorder, recorder)(context.Background(), c); err == nil {
		t.Fatal("multiple recorders accepted")
	}
	checker.result.By = ""
	if _, err := HookedPermission(nil, checker, recorder)(context.Background(), c); err == nil {
		t.Fatal("missing authority accepted")
	}
}
