package session

import (
	"reflect"
	"testing"

	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestRecorderResolvedPermissionSnapshot(t *testing.T) {
	s := createTyped(t, cleanStore(t), CreateOptions{ID: "resolved-permission"})
	call := ai.ToolCall{ID: "call", Name: "Bash", Input: `{}`}
	inference, err := s.StartInference(t.Context(), InferenceOptions{Purpose: PurposeTurn, TurnID: "turn", Provider: "test", Model: ai.Model{ID: "model"}, Request: &ai.Request{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := inference.Finish(t.Context(), &ai.Response{}, nil, Event{Payload: MessageAppendPayload{Messages: []ai.Message{{ID: "request", Role: ai.RoleAssistant, Content: ai.Content{ai.ToolCallBlock(call)}}}}}); err != nil {
		t.Fatal(err)
	}
	r := NewRecorder(s, "test")
	resolved := perm.Resolution{CallID: call.ID, Decision: "allow", By: "user", Scope: "session", Reason: "approved",
		Asked: &perm.Asked{Options: []string{"Allow once", "Allow for session", "Deny"}, Detail: []byte(`{"command":"actual shown command"}`), At: 123}}
	if err := r.RecordPermission(t.Context(), resolved); err != nil {
		t.Fatal(err)
	}
	resolved.Asked.Options[0], resolved.Asked.Detail[0] = "mutated", '['
	p := s.Snapshot()
	events := p.Events()
	decision := events[len(events)-1].Payload.(*PermissionDecidePayload)
	if decision.By != "user" || decision.Scope != "session" || decision.Asked.Options[0] != "Allow once" || string(decision.Asked.Detail) != `{"command":"actual shown command"}` || decision.Asked.At != 123 {
		t.Fatalf("lost final resolution: %+v", decision)
	}
}

func TestJudgePermissionFinishBatch(t *testing.T) {
	store := cleanStore(t)
	s := createTyped(t, store, CreateOptions{})
	main, err := s.StartInference(t.Context(), InferenceOptions{Purpose: PurposeTurn, TurnID: "turn", Provider: "test", Model: ai.Model{ID: "model"}, Request: &ai.Request{}})
	if err != nil {
		t.Fatal(err)
	}
	call := ai.ToolCall{ID: "call", Name: "Bash", Input: `{}`}
	if err := main.Finish(t.Context(), &ai.Response{}, nil, Event{Payload: MessageAppendPayload{Messages: []ai.Message{{ID: "request", Role: ai.RoleAssistant, Content: ai.Content{ai.ToolCallBlock(call)}}}}}); err != nil {
		t.Fatal(err)
	}
	c := s.Completion(PurposeJudge)
	finish, err := c.Admission()(t.Context(), "test", ai.Model{ID: "judge"}, &ai.Request{System: "judge"})
	if err != nil {
		t.Fatal(err)
	}
	if err := finish(t.Context(), &ai.Response{Usage: ai.Usage{Input: 17, Output: 4}}, nil); err != nil {
		t.Fatal(err)
	}
	snapshot := s.Snapshot()
	before := snapshot.Events()
	start := before[len(before)-1].Seq
	r := NewRecorder(s, "test")
	if err := r.RecordPermission(t.Context(), perm.Resolution{CallID: call.ID, Decision: "allow", By: ByJudge, Reason: "local test", Finish: c.FinishPermission}); err != nil {
		t.Fatal(err)
	}
	snapshot = s.Snapshot()
	events := snapshot.Events()
	decision, outcome := events[len(events)-2], events[len(events)-1]
	if decision.Type != EventPermissionDecide || decision.Source == nil || decision.Source.Seq != start || !reflect.DeepEqual(decision.Source, outcome.Source) {
		t.Fatalf("missing judge provenance: decision=%+v finish=%+v", decision, outcome)
	}
	p := outcome.Payload.(*InferenceFinishPayload)
	if p.Outcome != OutcomeSuccess || p.Usage.Input != 17 || p.Usage.Output != 4 || len(snapshot.Unfinished().Inferences) != 0 {
		t.Fatalf("lost judge accounting: %+v", p)
	}
	batches := recorderBatches(t, store, s)
	if got := batches[len(batches)-1]; !reflect.DeepEqual(got, []string{"permission.decide", "inference.finish"}) {
		t.Fatalf("non-atomic judge result: %v", got)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}
