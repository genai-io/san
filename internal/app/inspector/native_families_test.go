package inspector

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestHTTPAllThirteenNativeFamilies(t *testing.T) {
	store, w, srv := fixture(t)
	ctx := context.Background()
	appendPayload(t, w, session.InstructionsUpdatePayload{Set: map[string]session.InstructionSection{"base": {Content: "help"}}, Cause: "boot"})
	appendPayload(t, w, session.ToolsetUpdatePayload{Set: map[string]ai.Schema{"Read": {Name: "Read", Description: "read"}}, Cause: "boot"})
	appendPayload(t, w, session.SkillsUpdatePayload{Set: map[string]string{"review": session.SkillActive}, Cause: "user:/skills"})
	appendPayload(t, w, session.SessionUpdatePayload{State: &session.StatePatch{Title: session.SetField("title")}})
	appendPayload(t, w, session.MessageAppendPayload{Messages: []ai.Message{message("user", "question")}})
	request, err := store.ContextRequest(ctx, w.ID(), 0)
	if err != nil {
		t.Fatal(err)
	}
	start, err := w.StartInference(ctx, session.InferenceOptions{Purpose: session.PurposeTurn, TurnID: "turn", Provider: "test", Model: ai.Model{ID: "model"}, Request: request})
	if err != nil {
		t.Fatal(err)
	}
	call := ai.Message{ID: "call-message", Role: ai.RoleAssistant, Content: ai.Content{ai.ToolCallBlock(ai.ToolCall{ID: "call", Name: "Read", Input: `{"path":"file"}`})}}
	if err = start.Finish(ctx, &ai.Response{}, nil, session.Event{Payload: session.MessageAppendPayload{Messages: []ai.Message{call}}}); err != nil {
		t.Fatal(err)
	}
	appendPayload(t, w, session.PermissionDecidePayload{CallID: "call", Decision: "allow", By: session.ByConfig})
	result := ai.Message{ID: "result", Role: ai.RoleUser, Content: ai.Content{ai.ToolResultBlock(ai.ToolResult{ToolCallID: "call", ToolName: "Read", Content: ai.TextContent("result")})}}
	if err = w.Append(ctx, session.Event{Payload: session.MessageAppendPayload{Messages: []ai.Message{result}}}, session.Event{Payload: session.ToolFinishPayload{CallID: "call", Outcome: session.OutcomeSuccess, Details: json.RawMessage(`{"raw":"details"}`)}}); err != nil {
		t.Fatal(err)
	}
	appendPayload(t, w, session.HookFinishPayload{Hook: "audit", Trigger: "completion", Outcome: session.HookRan})
	appendPayload(t, w, session.AnnotationAddPayload{Text: "note", AfterMessage: "user"})
	request, err = store.ContextRequest(ctx, w.ID(), 0)
	if err != nil {
		t.Fatal(err)
	}
	compact, err := w.StartInference(ctx, session.InferenceOptions{Purpose: session.PurposeCompaction, Provider: "test", Model: ai.Model{ID: "model"}, Request: request})
	if err != nil {
		t.Fatal(err)
	}
	if err = compact.Finish(ctx, &ai.Response{}, nil, session.Event{Payload: session.ContextCompactPayload{Summary: message("summary", "summary")}}); err != nil {
		t.Fatal(err)
	}
	var raw rawRecords
	getJSON(t, srv, "/api/sessions/session/records", &raw)
	types := map[session.EventType]bool{}
	var last uint64
	var sources int
	for _, batch := range raw.Batches {
		var payload struct {
			Events []session.Event `json:"events"`
		}
		if err = json.Unmarshal(batch.Batch.Records[0].Data, &payload); err != nil {
			t.Fatal(err)
		}
		for _, event := range payload.Events {
			if event.Seq != last+1 {
				t.Fatal("event ordering differs")
			}
			last = event.Seq
			types[event.Type] = true
			if event.Source != nil {
				sources++
				if event.Source.Seq != start.Ref().Seq && event.Source.Seq != compact.Ref().Seq {
					t.Fatal("source provenance changed")
				}
			}
		}
	}
	if len(types) != 13 || sources < 4 {
		t.Fatalf("families=%d provenance links=%d", len(types), sources)
	}
}
