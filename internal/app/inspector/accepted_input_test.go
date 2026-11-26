package inspector

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestAcceptedAndConsumedInputRetainsFactsButProjectsOneMessage(t *testing.T) {
	store, owner, srv := fixture(t)
	initial := message("initial", "first prompt")
	queued := message("queued", "next prompt")
	appendPayload(t, owner, session.MessageAppendPayload{Messages: []ai.Message{initial}})
	request, err := store.ContextRequest(t.Context(), owner.ID(), 0)
	if err != nil {
		t.Fatal(err)
	}
	inference, err := owner.StartInference(t.Context(), session.InferenceOptions{Purpose: session.PurposeTurn, TurnID: "turn", Provider: "fixture", Model: ai.Model{ID: "fixture"}, Request: request})
	if err != nil {
		t.Fatal(err)
	}
	appendPayload(t, owner, session.MessageAppendPayload{Messages: []ai.Message{queued}, Accepted: true})
	before := owner.Snapshot()
	acceptance := before.Events()[len(before.Events())-1].Seq
	answer := ai.Message{ID: "answer", Role: ai.RoleAssistant, Content: ai.TextContent("first answer")}
	if err = inference.Finish(t.Context(), &ai.Response{Content: answer.Content}, nil, session.Event{Payload: session.MessageAppendPayload{Messages: []ai.Message{answer}}}); err != nil {
		t.Fatal(err)
	}
	if err = owner.Append(t.Context(), session.Event{Source: &session.EventRef{Seq: acceptance}, Payload: session.MessageAppendPayload{Messages: []ai.Message{queued}}}); err != nil {
		t.Fatal(err)
	}
	var latest nativeState
	getJSON(t, srv, "/api/sessions/session/state/0", &latest)
	want := []ai.Message{initial, answer, queued}
	if !reflect.DeepEqual(latest.History, want) || !reflect.DeepEqual(latest.Context.Messages, want) {
		t.Fatalf("accepted input was duplicated or reordered: history=%+v active=%+v", latest.History, latest.Context.Messages)
	}
	var admitted nativeState
	getJSON(t, srv, "/api/sessions/session/state/3", &admitted)
	if !reflect.DeepEqual(admitted.Request.Messages, []ai.Message{initial}) {
		t.Fatal("queued input leaked into an earlier admitted request")
	}
	var raw rawRecords
	getJSON(t, srv, "/api/sessions/session/records", &raw)
	var occurrences []session.Event
	for _, batch := range raw.Batches {
		var payload struct {
			Events []session.Event `json:"events"`
		}
		if err = json.Unmarshal(batch.Batch.Records[0].Data, &payload); err != nil {
			t.Fatal(err)
		}
		for _, event := range payload.Events {
			if p, ok := event.Payload.(*session.MessageAppendPayload); ok {
				for _, m := range p.Messages {
					if m.ID == queued.ID {
						occurrences = append(occurrences, event)
					}
				}
			}
		}
	}
	if len(occurrences) != 2 || !occurrences[0].Payload.(*session.MessageAppendPayload).Accepted || occurrences[1].Payload.(*session.MessageAppendPayload).Accepted || occurrences[1].Source == nil || occurrences[1].Source.Seq != acceptance {
		t.Fatalf("acceptance or consumption fact lost: %+v", occurrences)
	}
}
