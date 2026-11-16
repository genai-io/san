package session

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestDecodedReplayMatchesPublicReplay(t *testing.T) {
	f := fullEventFixture(t)
	want, err := ReplayEventBatches(testSession, f.batches, f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got *Projection
	for _, batch := range f.batches {
		raw, err := json.Marshal(eventBatchPayload{1, batch, f.ctx})
		if err != nil {
			t.Fatal(err)
		}
		payload, err := decodeEventBatchPayload(raw)
		if err != nil {
			t.Fatal(err)
		}
		got, err = applyDecodedEventBatch(got, testSession, payload.Events, payload.Validation)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(want.data, got.data) {
		t.Fatal("decoded replay changed projection")
	}
	// Public replay still detaches nested payloads from caller-owned batches.
	before := eventCopy(want.data)
	f.batches[1][0].Payload.(InstructionsUpdatePayload).Set["base"] = InstructionSection{Content: "changed"}
	f.batches[5][0].Payload.(MessageAppendPayload).Messages[0].Content[0].Text = "changed"
	if !reflect.DeepEqual(before, want.data) {
		t.Fatal("public replay retained caller payload")
	}
}

func TestDecodedReplayDiscardsFailedBuilder(t *testing.T) {
	batches := projectionBenchmarkBatches(3, 64)
	batches[2][0].Payload = MessageAppendPayload{Messages: batches[1][0].Payload.(MessageAppendPayload).Messages}
	var got *Projection
	for i, batch := range batches {
		raw, err := json.Marshal(eventBatchPayload{1, batch, EventValidationContext{}})
		if err != nil {
			t.Fatal(err)
		}
		payload, err := decodeEventBatchPayload(raw)
		if err != nil {
			t.Fatal(err)
		}
		got, err = applyDecodedEventBatch(got, testSession, payload.Events, payload.Validation)
		if i == len(batches)-1 {
			if err == nil || got != nil {
				t.Fatal("published partially applied invalid batch")
			}
		} else if err != nil {
			t.Fatal(err)
		}
	}
}
