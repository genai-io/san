package session

import (
	"reflect"
	"sync"
	"testing"

	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestEventProjectionBranchesOwnContainers(t *testing.T) {
	f := fullEventFixture(t)
	before := eventCopy(f.p.data)
	seq := uint64(len(before.Events) + 1)
	branch := func(text string) []Event {
		payloads := []EventPayload{
			MessageAppendPayload{Messages: []ai.Message{eventText("branch-message", ai.RoleUser, text)}},
			InstructionUpdatePayload{Upsert: []InstructionSection{{Name: "base", Text: text}}},
			ToolsetUpdatePayload{Upsert: []ai.Schema{{Name: "Read", Description: text}}},
			SkillUpdatePayload{Changes: []SkillActivation{{Name: "review", Active: false}}},
			StateUpdatePayload{Patch: StatePatch{Title: SetField(text)}},
			ConfigUpdatePayload{Patch: ConfigPatch{Model: SetField(text)}},
		}
		events := make([]Event, len(payloads))
		for i, payload := range payloads {
			events[i] = Event{ID: text + string(rune('a'+i)), SessionID: "session", Seq: seq + uint64(i), SchemaVersion: 1, Type: payload.eventType(), Payload: payload}
		}
		return events
	}
	var wg sync.WaitGroup
	for _, text := range []string{"left", "right"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			events := branch(text)
			p, err := ApplyEventBatch(f.p, events, f.ctx)
			if err != nil {
				t.Error(err)
				return
			}
			if p.message("branch-message").Content[0].Text != text {
				t.Error("branch message changed")
			}
			// Every accessor must remain detached even though accepted payloads are shared internally.
			messages := p.Messages()
			messages[len(messages)-1].Content[0].Text = "mutated"
			history := p.Events()
			history[0].Payload.(*SessionCreatePayload).AgentID = "mutated"
			*p.Config().Model = "mutated"
			*p.State().Title = "mutated"
			if p.message("branch-message").Content[0].Text != text || *p.data.Config.Model != text || *p.data.State.Title != text {
				t.Error("accessor aliases projection")
			}
			events[0].Payload.(MessageAppendPayload).Messages[0].Content[0].Text = "mutated"
			if p.message("branch-message").Content[0].Text != text {
				t.Error("incoming aliases projection")
			}
		}()
	}
	wg.Wait()
	if !reflect.DeepEqual(before, f.p.data) {
		t.Fatal("branches changed base")
	}
	if f.p.eventIndex["lefta"] != 0 || f.p.message("branch-message").ID != "" {
		t.Fatal("branch changed base indexes")
	}
	bad := branch("failed")
	bad = append(bad, Event{ID: "invalid", SessionID: "session", Seq: seq + uint64(len(bad)), SchemaVersion: 1, Type: EventAnnotationAdd, Payload: AnnotationAddPayload{}})
	if p, err := ApplyEventBatch(f.p, bad, f.ctx); err == nil || p != nil {
		t.Fatal("accepted failed batch")
	}
	if !reflect.DeepEqual(before, f.p.data) || f.p.message("branch-message").ID != "" {
		t.Fatal("failure changed base")
	}
}

func TestEventReplayDiscardsFailedBuilder(t *testing.T) {
	batches := projectionBenchmarkBatches(100, 64)
	batches[99][0].Payload = MessageAppendPayload{Messages: []ai.Message{eventText("message-1", ai.RoleUser, "duplicate")}}
	if p, err := ReplayEventBatches(batches, EventValidationContext{}); p != nil || err == nil {
		t.Fatal("published partial replay")
	}
}
