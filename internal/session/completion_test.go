package session

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestCompletionAcceptanceBatch(t *testing.T) {
	store := cleanStore(t)
	s := createTyped(t, store, CreateOptions{ID: "completion"})
	extra := []ai.Message{eventText("prompt", ai.RoleUser, "temporary")}
	c := s.Completion(PurposeRecap, extra...)
	extra[0].Content[0].Text = "mutated"
	finish, err := c.Admission()(t.Context(), "test", ai.Model{ID: "model"}, &ai.Request{Messages: []ai.Message{eventText("prompt", ai.RoleUser, "temporary")}})
	if err != nil {
		t.Fatal(err)
	}
	response := &ai.Response{Content: ai.TextContent("private output"), Usage: ai.Usage{Input: 17, Output: 4}}
	if err := finish(t.Context(), response, nil); err != nil {
		t.Fatal(err)
	}
	response.Usage.Input = 99
	before := s.Snapshot()
	if len(before.Events()) != 2 || len(before.Unfinished().Inferences) != 1 {
		t.Fatal("domain result was published before acceptance")
	}
	start := before.Events()[1].Payload.(*InferenceStartPayload)
	if start.Input.Conversation == nil || start.Input.Conversation.Append[0].Content.Text() != "temporary" {
		t.Fatal("completion append was not detached")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := c.Finish(ctx, Event{Payload: AnnotationAddPayload{Text: "delivered recap"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Finish(t.Context(), Event{Payload: AnnotationAddPayload{Text: "duplicate"}}); err == nil {
		t.Fatal("accepted duplicate result")
	}
	p := s.Snapshot()
	events := p.Events()
	if len(events) != 4 || events[2].Type != EventAnnotationAdd || events[3].Type != EventInferenceFinish ||
		events[2].Source == nil || *events[2].Source != *events[3].Source || events[2].Source.Seq != 2 {
		t.Fatalf("result/finish linkage: %+v", events)
	}
	outcome := events[3].Payload.(*InferenceFinishPayload)
	if outcome.Usage.Input != 17 || outcome.Outcome != OutcomeSuccess || len(p.Messages()) != 0 || len(p.Unfinished().Inferences) != 0 {
		t.Fatalf("lost accounting or invented output: %+v", outcome)
	}
	var sizes []int
	for batch, err := range readEventBatches(t.Context(), store.sessionDir(s.ID())) {
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := decodeEventBatchPayload(batch.Batch.Records[0].Data)
		if err != nil {
			t.Fatal(err)
		}
		sizes = append(sizes, len(decoded.Events))
	}
	if !reflect.DeepEqual(sizes, []int{1, 1, 2}) {
		t.Fatalf("result and finish were split: %v", sizes)
	}
}

func TestCompletionRetryAndDiscard(t *testing.T) {
	s := createTyped(t, cleanStore(t), CreateOptions{ID: "retries"})
	c := s.Completion(PurposeSuggest)
	admit := func() func(context.Context, *ai.Response, error) error {
		t.Helper()
		f, err := c.Admission()(t.Context(), "test", ai.Model{ID: "model"}, &ai.Request{System: "standalone"})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	f := admit()
	if err := f(t.Context(), &ai.Response{Content: ai.TextContent("partial"), Usage: ai.Usage{Input: 7}}, errors.New("provider failed")); err != nil {
		t.Fatal(err)
	}
	f = admit()
	p := s.Snapshot()
	if events := p.Events(); len(events) != 4 || events[2].Payload.(*InferenceFinishPayload).Outcome != OutcomeFailure {
		t.Fatalf("retry dispatched without closing failed attempt: %+v", events)
	}
	if err := f(t.Context(), &ai.Response{Content: ai.TextContent("discarded"), Usage: ai.Usage{Output: 3}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	p = s.Snapshot()
	if len(p.Events()) != 5 || len(p.Messages()) != 0 || len(p.Unfinished().Inferences) != 0 {
		t.Fatal("discard lost accounting or published output")
	}
	if err := f(t.Context(), &ai.Response{}, nil); err == nil {
		t.Fatal("accepted duplicate callback")
	}
}

func TestCompletionCloseWhileRunning(t *testing.T) {
	s := createTyped(t, cleanStore(t), CreateOptions{ID: "cancelled"})
	c := s.Completion(PurposeRecap)
	f, err := c.Admission()(t.Context(), "test", ai.Model{ID: "model"}, &ai.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := f(ctx, &ai.Response{Usage: ai.Usage{Input: 11}}, context.Canceled); err != nil {
		t.Fatal(err)
	}
	p := s.Snapshot()
	events := p.Events()
	outcome := events[len(events)-1].Payload.(*InferenceFinishPayload)
	if outcome.Outcome != OutcomeCancelled || outcome.Usage.Input != 11 || len(p.Unfinished().Inferences) != 0 {
		t.Fatalf("lost cancelled attempt: %+v", outcome)
	}
	if _, err := c.Admission()(t.Context(), "test", ai.Model{ID: "model"}, &ai.Request{}); err == nil {
		t.Fatal("discarded completion retried")
	}
}

func TestCompletionHookWithoutInference(t *testing.T) {
	s := createTyped(t, cleanStore(t), CreateOptions{ID: "hook"})
	c := s.Completion(PurposeHook)
	if err := c.FinishHook(t.Context(), hook.HookFiredAudit{Outcome: "async"}); err == nil {
		t.Fatal("accepted async launch as completion")
	}
	if err := c.FinishHook(t.Context(), hook.HookFiredAudit{Event: "PreToolUse", Source: "verify", Outcome: HookError, Reason: "no provider"}); err != nil {
		t.Fatal(err)
	}
	p := s.Snapshot()
	if events := p.Events(); len(events) != 2 || events[1].Type != EventHookFinish || events[1].Source != nil {
		t.Fatalf("invented inference: %+v", events)
	}
}
