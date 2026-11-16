package session

import (
	"testing"

	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestFlowCompletionPurpose(t *testing.T) {
	store := cleanStore(t)
	owner := createTyped(t, store, CreateOptions{ID: "flow-purpose"})
	c := owner.Completion(PurposeFlow, eventText("goal", ai.RoleUser, "draft only"))
	finish, err := c.Admission()(t.Context(), "test", ai.Model{ID: "model"}, &ai.Request{Messages: []ai.Message{eventText("goal", ai.RoleUser, "draft only")}})
	if err != nil {
		t.Fatal(err)
	}
	if err := finish(t.Context(), &ai.Response{Content: ai.TextContent("blueprint")}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Finish(t.Context()); err != nil {
		t.Fatal(err)
	}
	p := owner.Snapshot()
	start := p.Events()[1].Payload.(*InferenceStartPayload)
	if start.Purpose != PurposeFlow {
		t.Fatalf("purpose = %q", start.Purpose)
	}
	if len(p.Messages()) != 0 || len(p.Unfinished().Inferences) != 0 {
		t.Fatal("draft entered conversation or remained open")
	}
}
