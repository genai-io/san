package session

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/tool/toolresult"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

type recorderTool struct {
	ran     *int
	details any
}

func (recorderTool) Schema() ai.Schema {
	return ai.Schema{Name: "work", Description: "test tool", Definition: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}}
}
func (t recorderTool) Run(context.Context, ai.ToolCall) (sdkagent.Result, error) {
	*t.ran++
	r := sdkagent.TextResult("raw output")
	r.Details = t.details
	return r, nil
}

func recorderAgent(r *Recorder, d ai.Driver, mutate func(*core.Config)) core.Agent {
	client := ai.NewClientWithDriver(d, ai.Model{ID: "stub", API: "stub", ContextWindow: 200000})
	c := core.Config{ID: "main", Client: func([]core.Message) (*ai.Client, error) { return client, nil }, System: core.NewSystem(), Tools: core.NewTools(), OutboxBuf: -1, InferenceAdmission: r.InferenceAdmission, ToolAdmission: r.ToolAdmission, OutputAdmission: r.OutputAdmission, OnEvent: r.RecordEvent}
	if mutate != nil {
		mutate(&c)
	}
	return core.NewAgent(c)
}
func recorderRun(t *testing.T, a core.Agent) error {
	t.Helper()
	a.Append(core.UserMessage("go", nil))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := a.ThinkAct(ctx)
	return err
}
func recorderSnapshot(s *Session) *Projection {
	p := s.Snapshot()
	return &p
}

func recorderTypes(s *Session) []string {
	var got []string
	for _, e := range recorderSnapshot(s).Events() {
		got = append(got, string(e.Type))
	}
	return got
}
func recorderGolden(t *testing.T, s *Session, want ...string) {
	t.Helper()
	if got := recorderTypes(s); !reflect.DeepEqual(got, want) {
		t.Fatalf("events\n got %q\nwant %q", got, want)
	}
}
func recorderBatches(t *testing.T, store *Store, s *Session) [][]string {
	t.Helper()
	var out [][]string
	for b, err := range readEventBatches(context.Background(), store.sessionDir(s.ID())) {
		if err != nil {
			t.Fatal(err)
		}
		p, err := decodeEventBatchPayload(b.Batch.Records[0].Data)
		if err != nil {
			t.Fatal(err)
		}
		var types []string
		for _, e := range p.Events {
			types = append(types, string(e.Type))
		}
		out = append(out, types)
	}
	return out
}

func TestRecorderSuccessfulTurns(t *testing.T) {
	store := cleanStore(t)
	s := createTyped(t, store, CreateOptions{})
	r := NewRecorder(s, "test")
	d := aitest.Always(aitest.Says("done"))
	a := recorderAgent(r, d, nil)
	for range 2 {
		if err := recorderRun(t, a); err != nil {
			t.Fatal(err)
		}
	}
	recorderGolden(t, s, "session.create", "message.append", "inference.start", "message.append", "inference.finish", "message.append", "inference.start", "message.append", "inference.finish")
	var starts []InferenceStartPayload
	for _, e := range recorderSnapshot(s).Events() {
		if p, ok := e.Payload.(*InferenceStartPayload); ok {
			starts = append(starts, *p)
		}
	}
	if len(starts) != 2 || starts[0].TurnID == "" || starts[0].TurnID == starts[1].TurnID {
		t.Fatalf("turn IDs: %+v", starts)
	}
	for _, m := range a.Messages() {
		r.RecordEvent(sdkagent.MessageAdded{Message: m})
	}
	if r.Err() != nil {
		t.Fatal(r.Err())
	}
	if len(recorderSnapshot(s).Messages()) != 4 {
		t.Fatal("duplicate confirmations persisted")
	}
	batches := recorderBatches(t, store, s)
	if !reflect.DeepEqual(batches[3], []string{"message.append", "inference.finish"}) {
		t.Fatalf("model outcome batch: %v", batches)
	}
}

func TestRecorderToolOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		deny, unknown, invalid bool
		details                any
	}{
		{name: "bash", details: toolresult.BashDetails{LineCount: 19, Tail: "raw tail"}},
		{name: "agent", details: toolresult.AgentDetails{Identity: toolresult.AgentIdentity{SessionID: "child-session"}}},
		{name: "denied", deny: true}, {name: "unknown", unknown: true}, {name: "invalid", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := cleanStore(t)
			s := createTyped(t, store, CreateOptions{})
			r := NewRecorder(s, "test")
			ran := 0
			name := "work"
			if tc.unknown {
				name = "missing"
			}
			input := "{}"
			if tc.invalid {
				input = "not json"
			}
			d := aitest.New(aitest.Asks(ai.ToolCall{ID: "call", Name: name, Input: input}), aitest.Says("done"))
			a := recorderAgent(r, d, func(c *core.Config) {
				c.Tools = core.NewTools(recorderTool{&ran, tc.details})
				c.Gate = func(ctx context.Context, p sdkagent.PreToolContext) (sdkagent.Decision, error) {
					decision := "allow"
					if tc.deny {
						decision = "deny"
					}
					err := r.Decide(ctx, PermissionDecidePayload{CallID: p.Call.ID, Decision: decision, By: ByConfig})
					return sdkagent.Decision{Block: tc.deny, Reason: "policy"}, err
				}
				c.ResultFilter = func(context.Context, sdkagent.PostToolContext) (*sdkagent.Result, error) {
					v := sdkagent.TextResult("filtered")
					return &v, nil
				}
			})
			if err := recorderRun(t, a); err != nil {
				t.Fatal(err)
			}
			if r.Err() != nil {
				t.Fatal(r.Err())
			}
			want := []string{"session.create", "toolset.update", "message.append", "inference.start", "message.append", "inference.finish"}
			if !tc.unknown && !tc.invalid {
				want = append(want, "permission.decide")
			}
			want = append(want, "message.append")
			if !tc.deny && !tc.unknown && !tc.invalid {
				want = append(want, "tool.finish")
			}
			want = append(want, "inference.start", "message.append", "inference.finish")
			recorderGolden(t, s, want...)
			finishes := 0
			for _, e := range recorderSnapshot(s).Events() {
				if p, ok := e.Payload.(*ToolFinishPayload); ok {
					finishes++
					raw, _ := json.Marshal(tc.details)
					if string(p.Details) != string(raw) {
						t.Fatalf("raw details %s, want %s", p.Details, raw)
					}
					if tc.name == "agent" && p.ChildSession != "child-session" {
						t.Fatal("child session lost")
					}
				}
			}
			if tc.deny || tc.unknown || tc.invalid {
				if ran != 0 || finishes != 0 {
					t.Fatal("invented execution")
				}
			} else {
				if ran != 1 || finishes != 1 {
					t.Fatal("missing execution")
				}
				found := false
				for _, b := range recorderBatches(t, store, s) {
					if reflect.DeepEqual(b, []string{"message.append", "tool.finish"}) {
						found = true
					}
				}
				if !found {
					t.Fatal("tool finish not atomic with message")
				}
				if got := d.Last().Messages[2].ToolResults()[0].Content; !reflect.DeepEqual(got, ai.TextContent("filtered")) {
					t.Fatalf("filtered content: %+v", got)
				}
			}
		})
	}
}

func TestRecorderStorageBoundaries(t *testing.T) {
	for _, boundary := range []string{"inference", "output", "tool"} {
		t.Run(boundary, func(t *testing.T) {
			s := createTyped(t, cleanStore(t), CreateOptions{})
			r := NewRecorder(s, "test")
			ran := 0
			d := aitest.New(aitest.Asks(ai.ToolCall{ID: "call", Name: "work", Input: "{}"}), aitest.Says("done"))
			a := recorderAgent(r, d, func(c *core.Config) {
				c.Tools = core.NewTools(recorderTool{ran: &ran})
				switch boundary {
				case "inference":
					c.InferenceAdmission = func(ctx context.Context, p sdkagent.InferenceAdmission) error {
						_ = s.Close()
						return r.InferenceAdmission(ctx, p)
					}
				case "output":
					c.OutputAdmission = func(ctx context.Context, p sdkagent.OutputAdmission) error {
						_ = s.Close()
						return r.OutputAdmission(ctx, p)
					}
				}
				c.Gate = func(ctx context.Context, p sdkagent.PreToolContext) (sdkagent.Decision, error) {
					if err := r.Decide(ctx, PermissionDecidePayload{CallID: p.Call.ID, Decision: "allow", By: ByConfig}); err != nil {
						return sdkagent.Decision{}, err
					}
					if boundary == "tool" {
						_ = s.Close()
					}
					return sdkagent.Decision{}, nil
				}
			})
			if err := recorderRun(t, a); err == nil {
				t.Fatal("storage failure did not stop turn")
			}
			if ran != 0 {
				t.Fatal("tool dispatched past failed fence")
			}
			if boundary == "inference" && d.Calls() != 0 {
				t.Fatal("model dispatched past failed fence")
			}
			if !errors.Is(r.InferenceAdmission(context.Background(), sdkagent.InferenceAdmission{}), ErrClosed) {
				t.Fatal("next admission not blocked")
			}
			want := 1
			if boundary == "tool" {
				want = 2
			}
			if len(recorderSnapshot(s).Messages()) != want {
				t.Fatalf("uncommitted output published: %+v", recorderSnapshot(s).Messages())
			}
		})
	}
}

func TestRecorderObserverFailureBlocksNextAdmission(t *testing.T) {
	s := createTyped(t, cleanStore(t), CreateOptions{})
	r := NewRecorder(s, "test")
	r.RecordEvent(sdkagent.MessageAdded{Message: eventText("bad", ai.RoleAssistant, "bypass")})
	if r.Err() == nil {
		t.Fatal("observer error not latched")
	}
	if err := r.InferenceAdmission(context.Background(), sdkagent.InferenceAdmission{}); err == nil {
		t.Fatal("next model admission allowed")
	}
	recorderGolden(t, s, "session.create")
}

func TestRecorderRetries(t *testing.T) {
	s := createTyped(t, cleanStore(t), CreateOptions{})
	r := NewRecorder(s, "test")
	d := aitest.New(aitest.Fails(&ai.Error{Kind: ai.KindNetwork, Message: "stream stalled"}), aitest.Says("done"))
	a := recorderAgent(r, d, nil)
	if err := recorderRun(t, a); err != nil {
		t.Fatal(err)
	}
	recorderGolden(t, s, "session.create", "message.append", "inference.start", "inference.finish", "inference.start", "message.append", "inference.finish")
	p := s.Snapshot()
	events := p.Events()
	first := events[2].Payload.(*InferenceStartPayload)
	second := events[4].Payload.(*InferenceStartPayload)
	if first.TurnID == "" || first.TurnID != second.TurnID {
		t.Fatal("retry lost turn identity")
	}
	finish := events[3].Payload.(*InferenceFinishPayload)
	if finish.Outcome != OutcomeFailure || finish.Error == nil || !finish.Error.Retryable {
		t.Fatalf("retry failure: %+v", finish)
	}
	if events[3].Source == nil || events[3].Source.Seq != events[2].Seq || events[6].Source == nil || events[6].Source.Seq != events[4].Seq {
		t.Fatal("retry outcomes not independently correlated")
	}
}

func TestRecorderCancelledAfterInference(t *testing.T) {
	s := createTyped(t, cleanStore(t), CreateOptions{})
	r := NewRecorder(s, "test")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := aitest.Always(aitest.Replies(ai.Response{Content: ai.TextContent("done"), Usage: ai.Usage{Input: 17, Output: 4}}))
	a := recorderAgent(r, d, func(c *core.Config) {
		c.OnEvent = func(e core.Event) {
			r.RecordEvent(e)
			if end, ok := e.(sdkagent.InferenceEnd); ok && end.Err == nil {
				cancel()
			}
		}
	})
	a.Append(core.UserMessage("go", nil))
	if _, err := a.ThinkAct(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("turn error: %v", err)
	}
	recorderGolden(t, s, "session.create", "message.append", "inference.start", "inference.finish")
	p := s.Snapshot()
	finish := p.Events()[3].Payload.(*InferenceFinishPayload)
	if finish.Outcome != OutcomeCancelled {
		t.Fatalf("finish: %+v", finish)
	}
	if finish.Usage == nil || finish.Usage.Input != 17 || finish.Usage.Output != 4 {
		t.Fatalf("cancelled inference lost usage: %+v", finish.Usage)
	}
	if len(p.Messages()) != 1 {
		t.Fatal("partial model message persisted")
	}
}

func TestRecorderExecutedOutcomeSurvivesPostToolFailure(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		name := "filter-error"
		if cancelled {
			name = "cancel-after-run"
		}
		t.Run(name, func(t *testing.T) {
			store := cleanStore(t)
			s := createTyped(t, store, CreateOptions{})
			r := NewRecorder(s, "test")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ran := 0
			raw := toolresult.BashDetails{LineCount: 3, Tail: "durable side effect"}
			sentinel := errors.New("post-tool failure")
			d := aitest.New(aitest.Asks(ai.ToolCall{ID: "executed", Name: "work", Input: "{}"}, ai.ToolCall{ID: "queued", Name: "work", Input: "{}"}), aitest.Says("not reached"))
			a := recorderAgent(r, d, func(c *core.Config) {
				c.Tools = core.NewTools(recorderTool{ran: &ran, details: raw})
				c.Gate = func(ctx context.Context, p sdkagent.PreToolContext) (sdkagent.Decision, error) {
					return sdkagent.Decision{}, r.Decide(ctx, PermissionDecidePayload{CallID: p.Call.ID, Decision: "allow", By: ByConfig})
				}
				c.ResultFilter = func(context.Context, sdkagent.PostToolContext) (*sdkagent.Result, error) {
					if cancelled {
						cancel()
						return nil, context.Canceled
					}
					return nil, sentinel
				}
			})
			a.Append(core.UserMessage("go", nil))
			_, err := a.ThinkAct(ctx)
			wantErr := sentinel
			if cancelled {
				wantErr = context.Canceled
			}
			if !errors.Is(err, wantErr) {
				t.Fatalf("turn error: %v", err)
			}
			recorderGolden(t, s, "session.create", "toolset.update", "message.append", "inference.start", "message.append", "inference.finish", "permission.decide", "permission.decide", "tool.finish")
			p := s.Snapshot()
			if ran != 1 || d.Calls() != 1 {
				t.Fatalf("dispatch: tools=%d models=%d", ran, d.Calls())
			}
			if len(p.Messages()) != 2 {
				t.Fatal("invented tool-result message")
			}
			finish := p.Events()[8].Payload.(*ToolFinishPayload)
			if finish.CallID != "executed" || finish.Outcome != OutcomeSuccess {
				t.Fatalf("known executed result lost: %+v", finish)
			}
			details, _ := json.Marshal(raw)
			if string(finish.Details) != string(details) {
				t.Fatalf("raw details lost: %s", finish.Details)
			}
			batches := recorderBatches(t, store, s)
			if !reflect.DeepEqual(batches[len(batches)-1], []string{"tool.finish"}) {
				t.Fatalf("terminal batch: %v", batches)
			}
		})
	}
}

func TestRecorderInitialCatalogueOrder(t *testing.T) {
	s := createTyped(t, cleanStore(t), CreateOptions{})
	r := NewRecorder(s, "test")
	system := core.NewSystem()
	for _, entry := range []struct{ name, text string }{{"z", "first"}, {"a", "second"}, {"empty", ""}} {
		text := entry.text
		system.Use(core.Section{Name: entry.name, Slot: 5, Render: func() string { return text }}, "system:init")
	}
	d := aitest.Always(aitest.Says("done"))
	a := recorderAgent(r, d, func(c *core.Config) { c.System = system })
	if err := recorderRun(t, a); err != nil {
		t.Fatal(err)
	}
	if got := d.Last().System; got != system.Prompt() {
		t.Fatalf("initial catalogue projection differs: %q want %q", got, system.Prompt())
	}
	recorderGolden(t, s, "session.create", "instructions.update", "instructions.update", "message.append", "inference.start", "message.append", "inference.finish")
	p := s.Snapshot()
	start := p.Events()[4].Payload.(*InferenceStartPayload)
	if start.Input.Conversation == nil {
		t.Fatal("same-slot catalogue order forced standalone request")
	}
}

func TestRecorderCatalogueNoopsAndExplicitFacts(t *testing.T) {
	s := createTyped(t, cleanStore(t), CreateOptions{})
	r := NewRecorder(s, "test")
	ctx := context.Background()
	for range 2 {
		r.RecordEvent(core.SystemChange{Name: "one", Content: "first", Slot: 5, Caller: "system:init"})
		r.RecordEvent(core.ToolsChange{Schema: ai.Schema{Name: "work"}, Caller: "tools:init"})
	}
	r.RecordEvent(core.SystemChange{Name: "empty", Content: "", Caller: "system:init"})
	for range 2 {
		if err := r.RecordSkill(ctx, "test", SkillActive, "user:test"); err != nil {
			t.Fatal(err)
		}
		if err := r.RecordState(ctx, SessionUpdatePayload{State: &StatePatch{Title: SetField("title")}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.RecordHook(ctx, hook.HookFiredAudit{Outcome: "async"}); err != nil {
		t.Fatal(err)
	}
	recorderGolden(t, s, "session.create", "instructions.update", "toolset.update", "skills.update", "session.update")
	if r.Err() != nil {
		t.Fatal(r.Err())
	}
}
