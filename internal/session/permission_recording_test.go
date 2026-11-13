package session

import (
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
