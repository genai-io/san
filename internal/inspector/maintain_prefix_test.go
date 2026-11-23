package inspector

import (
	"errors"
	"testing"

	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestMaintainPrefixComparison(t *testing.T) {
	provider, model, other := "provider", "model", "other"
	route := session.EventConfig{Provider: &provider, Model: &model}
	main := &ai.Request{System: "system", Tools: []ai.Schema{{Name: "tool"}}, Messages: []ai.Message{ai.UserMessage("question")}}
	answer := ai.Message{ID: "recorded", Role: ai.RoleAssistant, Content: ai.TextContent("answer")}
	for _, tc := range []struct {
		name, want string
		change     func(*ai.Request, *session.InferenceStartPayload)
	}{
		{"match despite next admission", "match", nil},
		{"system mismatch", "mismatch", func(r *ai.Request, _ *session.InferenceStartPayload) { r.System = "different" }},
		{"tool mismatch", "mismatch", func(r *ai.Request, _ *session.InferenceStartPayload) { r.Tools = nil }},
		{"message mismatch", "mismatch", func(r *ai.Request, _ *session.InferenceStartPayload) { r.Messages[0] = ai.UserMessage("different") }},
		{"legacy standalone", "mismatch", func(r *ai.Request, _ *session.InferenceStartPayload) {
			r.Messages = []ai.Message{ai.UserMessage("legacy review")}
		}},
		{"route mismatch", "route-mismatch", func(_ *ai.Request, s *session.InferenceStartPayload) { s.Request.Model = &other }},
		{"unavailable request", "unavailable", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := &session.InferenceStartPayload{Purpose: session.PurposeMemoryUpdate, Request: route}
			start.Input.Standalone = "test-standalone"
			maintain := *main
			maintain.Messages = []ai.Message{main.Messages[0], {Role: ai.RoleAssistant, Content: answer.Content}, ai.UserMessage("review")}
			if tc.change != nil {
				tc.change(&maintain, start)
			}
			end := &session.InferenceFinishPayload{Outcome: session.OutcomeSuccess, StopReason: string(ai.StopEndTurn)}
			events := []session.Event{
				{Seq: 1, Payload: &session.InferenceStartPayload{Purpose: session.PurposeTurn, Request: route}},
				{Seq: 2, Source: &session.EventRef{Seq: 1}, Payload: &session.MessageAppendPayload{Messages: []ai.Message{answer}}},
				{Seq: 3, Source: &session.EventRef{Seq: 1}, Payload: end},
				{Seq: 4, Payload: &session.InferenceStartPayload{Purpose: session.PurposeTurn, Request: route}},
				{Seq: 5, Payload: start},
				{Seq: 6, Source: &session.EventRef{Seq: 4}, Payload: end},
			}
			got := compareMaintainPrefix(events, "test", 5, &maintain, func(seq uint64) (*ai.Request, error) {
				if seq != 1 {
					t.Fatalf("loaded request %d, want 1", seq)
				}
				if tc.want == "unavailable" {
					return nil, errors.New("missing blob")
				}
				return main, nil
			})
			want := prefixComparison{Status: tc.want}
			if tc.want == "match" {
				want.Source, want.Count = 1, 2
			}
			if got != want {
				t.Fatalf("got %+v, want %+v", got, want)
			}
		})
	}
}
