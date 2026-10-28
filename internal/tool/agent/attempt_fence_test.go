package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/genai-io/san/internal/task"
)

func TestStopApprovalCannotCancelLaterAttempt(t *testing.T) {
	worker := endedContinuationTask(t, task.StatusRunning)
	stopper := NewAgentStopTool()
	ctx := task.WithSessionID(context.Background(), "parent")
	params := map[string]any{"task_id": worker.GetID()}
	if _, err := stopper.PreparePermission(ctx, params, "."); err != nil {
		t.Fatal(err)
	}
	worker.Complete(errors.New("old execution failed"))
	next, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := worker.BeginAttempt("parent", next, cancel); err != nil {
		t.Fatal(err)
	}
	result := stopper.ExecuteApproved(ctx, params, ".")
	if result.Success || next.Err() != nil || !worker.IsRunning() {
		t.Fatalf("stale stop approval canceled successor: %+v, context=%v", result, next.Err())
	}
	worker.CompleteAttempt(2, nil)
}
