package task

import (
	"context"
	"testing"
)

func TestAgentSourceMessageSeqAttemptFence(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := NewAgentTask("source", "", "", ctx, cancel, "")
	worker.SetSourceMessageSeq(1, 42)
	if got := worker.GetStatus().SourceMessageSeq; got != 42 {
		t.Fatalf("source=%d", got)
	}
	worker.Complete(nil)
	next, err := worker.BeginAttempt("", ctx, cancel)
	if err != nil {
		t.Fatal(err)
	}
	if got := worker.GetStatus().SourceMessageSeq; got != 0 {
		t.Fatalf("stale source=%d", got)
	}
	worker.SetSourceMessageSeq(1, 42)
	if got := worker.GetStatus().SourceMessageSeq; got != 0 {
		t.Fatalf("old attempt replaced source=%d", got)
	}
	worker.SetSourceMessageSeq(next, 84)
	if got := worker.GetStatus().SourceMessageSeq; got != 84 {
		t.Fatalf("source=%d", got)
	}
	worker.Complete(nil)
}
