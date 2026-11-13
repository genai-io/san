package session

import (
	"context"
	"errors"
	"testing"

	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestCompletionDiscardAndWait(t *testing.T) {
	s := createTyped(t, cleanStore(t), CreateOptions{ID: "join-compaction"})
	unfinishedCount := func() int { p := s.Snapshot(); return len(p.Unfinished().Inferences) }
	c := s.Completion(PurposeCompaction)
	finish, err := c.Admission()(t.Context(), "test", ai.Model{ID: "model"}, &ai.Request{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := c.DiscardAndWait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait: %v", err)
	}
	if unfinishedCount() != 1 {
		t.Fatal("invented terminal event before provider returned")
	}
	if _, err := c.Admission()(t.Context(), "test", ai.Model{ID: "model"}, &ai.Request{}); err == nil {
		t.Fatal("admitted retry during retirement")
	}
	done := make(chan error, 1)
	go func() { done <- c.DiscardAndWait(t.Context()) }()
	if err := finish(context.Background(), &ai.Response{Usage: ai.Usage{Input: 9}}, context.Canceled); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if unfinishedCount() != 0 {
		t.Fatal("joined before terminal write")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCompletionDiscardAndWaitUnstartedAndAccepted(t *testing.T) {
	s := createTyped(t, cleanStore(t), CreateOptions{ID: "join-terminal"})
	eventCount := func() int { p := s.Snapshot(); return len(p.Events()) }
	unstarted := s.Completion(PurposeCompaction)
	if err := unstarted.DiscardAndWait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := unstarted.Admission()(t.Context(), "test", ai.Model{ID: "model"}, &ai.Request{}); err == nil {
		t.Fatal("retired unstarted command admitted")
	}
	c := s.Completion(PurposeCompaction)
	finish, err := c.Admission()(t.Context(), "test", ai.Model{ID: "model"}, &ai.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if err := finish(t.Context(), &ai.Response{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Finish(t.Context(), Event{Payload: AnnotationAddPayload{Text: "accepted"}}); err != nil {
		t.Fatal(err)
	}
	before := eventCount()
	if err := c.DiscardAndWait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if eventCount() != before {
		t.Fatal("changed accepted terminal result")
	}
}
