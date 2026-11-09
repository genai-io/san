package session

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestInferenceRequestsAndFinish(t *testing.T) {
	ctx := context.Background()
	s := createTyped(t, cleanStore(t), CreateOptions{ID: "inference"})
	if err := s.Append(ctx, Event{Payload: MessageAppendPayload{Messages: []ai.Message{eventText("u", ai.RoleUser, "question")}}}); err != nil {
		t.Fatal(err)
	}
	p := s.Snapshot()
	r := conversationRequest(&p, nil)
	zero := 0.0
	r.Temperature = &zero
	r.ToolChoice = ai.ToolChoiceNamed("native")
	r.Effort = ai.EffortHigh
	r.CacheRetention = ai.CacheLong
	r.StopSequences = []string{"stop"}
	r.Schema = &ai.Schema{Name: "answer"}
	r.SamplingParams = map[string]any{"seed": json.Number("9007199254740993")}
	h, err := s.StartInference(ctx, InferenceOptions{Purpose: PurposeTurn, TurnID: "turn", Provider: "test", Model: ai.Model{ID: "model"}, Request: r})
	if err != nil {
		t.Fatal(err)
	}
	p = s.Snapshot()
	start := p.event(h.Ref().Seq).Payload.(*InferenceStartPayload)
	if start.Input.Conversation == nil {
		t.Fatal("expected conversation")
	}
	if err = s.Append(ctx, Event{Payload: InstructionsUpdatePayload{Set: map[string]InstructionSection{"new": {Content: "later"}}, Cause: "test"}}); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := s.InferenceRequest(ctx, h.Ref().Seq)
	if err != nil {
		t.Fatal(err)
	}
	if !sameJSON(r, rebuilt) {
		t.Fatalf("request changed: %#v", rebuilt)
	}
	before := len(p.Events()) + 1
	if err = h.Finish(ctx, &ai.Response{}, nil, Event{Payload: MessageAppendPayload{Messages: []ai.Message{eventText("a", ai.RoleAssistant, "answer")}}}, Event{Payload: AnnotationAddPayload{}}); err == nil {
		t.Fatal("invalid result accepted")
	}
	p = s.Snapshot()
	if len(p.Events()) != before {
		t.Fatal("partial batch")
	}
	if err = h.Finish(ctx, &ai.Response{}, nil); err != nil {
		t.Fatal(err)
	}
	if err = h.Finish(ctx, &ai.Response{}, nil); err == nil {
		t.Fatal("duplicate finish")
	}
	p = s.Snapshot()
	if len(p.Messages()) != 1 {
		t.Fatal("invented output")
	}
	r.System = "adapted"
	h, err = s.StartInference(ctx, InferenceOptions{Purpose: PurposeSuggest, Provider: "test", Model: ai.Model{ID: "model"}, Request: r})
	if err != nil {
		t.Fatal(err)
	}
	p = s.Snapshot()
	if p.event(h.Ref().Seq).Payload.(*InferenceStartPayload).Input.Standalone == "" {
		t.Fatal("expected standalone")
	}
	rebuilt, err = s.InferenceRequest(ctx, h.Ref().Seq)
	if err != nil || rebuilt.System != r.System || !sameJSON(rebuilt.Messages, r.Messages) || !sameJSON(rebuilt.Tools, r.Tools) || !sameJSON(rebuilt.SamplingParams, r.SamplingParams) {
		t.Fatalf("standalone roundtrip: %v", err)
	}
	failure := errors.New(strings.Repeat("界", maxReason))
	if err = h.Finish(ctx, &ai.Response{StopReason: ai.StopReason("length")}, failure); err != nil {
		t.Fatal(err)
	}
	p = s.Snapshot()
	finish := p.data.Events[len(p.data.Events)-1].Payload.(*InferenceFinishPayload)
	if finish.Usage == nil || finish.StopReason != "length" || len(finish.Error.Message) > maxReason || !utf8.ValidString(finish.Error.Message) {
		t.Fatalf("lost failure facts: %+v", finish)
	}
}

func TestInferenceCompactionCapturedPrefix(t *testing.T) {
	ctx := context.Background()
	s := createTyped(t, cleanStore(t), CreateOptions{ID: "compact-inference"})
	if err := s.Append(ctx, Event{Payload: MessageAppendPayload{Messages: []ai.Message{eventText("old", ai.RoleUser, "old")}}}); err != nil {
		t.Fatal(err)
	}
	p := s.Snapshot()
	extra := []ai.Message{eventText("extra", ai.RoleUser, "temporary")}
	r := conversationRequest(&p, extra)
	h, err := s.StartInference(ctx, InferenceOptions{Purpose: PurposeCompaction, Provider: "test", Model: ai.Model{ID: "model"}, Request: r, Append: extra})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Append(ctx, Event{Payload: MessageAppendPayload{Messages: []ai.Message{eventText("later", ai.RoleUser, "later")}}}); err != nil {
		t.Fatal(err)
	}
	compact := &ContextCompactPayload{Summary: eventText("summary", ai.RoleUser, "summary")}
	if err = h.Finish(ctx, &ai.Response{}, nil, Event{Payload: compact}); err != nil {
		t.Fatal(err)
	}
	if compact.Through != 0 {
		t.Fatal("mutated caller compaction")
	}
	p = s.Snapshot()
	active := p.ActiveMessages()
	if len(active) != 2 || active[1].ID != "later" {
		t.Fatalf("lost later append: %+v", active)
	}
	rebuilt, err := s.InferenceRequest(ctx, h.Ref().Seq)
	if err != nil || !sameJSON(rebuilt, r) {
		t.Fatalf("old prefix changed: %v", err)
	}
	for _, m := range p.Messages() {
		if m.ID == "extra" {
			t.Fatal("persisted temporary append")
		}
	}
}

func TestInferenceConcurrentStarts(t *testing.T) {
	s := createTyped(t, cleanStore(t), CreateOptions{ID: "concurrent-inference"})
	var wg sync.WaitGroup
	refs := make(chan uint64, 12)
	for range 12 {
		wg.Go(func() {
			h, err := s.StartInference(context.Background(), InferenceOptions{Purpose: PurposeSuggest, Provider: "test", Model: ai.Model{ID: "model"}, Request: &ai.Request{System: "standalone"}})
			if err != nil {
				t.Error(err)
				return
			}
			refs <- h.Ref().Seq
		})
	}
	wg.Wait()
	close(refs)
	seen := map[uint64]bool{}
	for seq := range refs {
		if seen[seq] {
			t.Fatal("reused reference")
		}
		seen[seq] = true
	}
	if len(seen) != 12 {
		t.Fatal(len(seen))
	}
}
