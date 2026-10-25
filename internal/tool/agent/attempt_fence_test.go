package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/task"
)

func TestContinuationApprovalCannotStartLaterAttempt(t *testing.T) {
	worker := endedContinuationTask(t, task.StatusFailed)
	agent := NewAgentTool()
	agent.SetExecutor(&continuationExecutor{recordingExecutor: recordingExecutor{configOK: true}})
	ctx := task.WithSessionID(context.Background(), "parent")
	params := map[string]any{"resume_task_id": worker.GetID(), "description": "Continue", "prompt": "Verify existing work"}
	if _, err := agent.PreparePermission(ctx, params, "."); err != nil {
		t.Fatal(err)
	}
	attempt, err := worker.BeginAttempt("parent", context.Background(), func() {})
	if err != nil {
		t.Fatal(err)
	}
	worker.CompleteAttempt(attempt, nil)
	if result := agent.ExecuteApproved(ctx, params, "."); result.Success || !strings.Contains(result.Error, "changed while continuation approval") {
		t.Fatalf("stale approval continued a later execution: %+v", result)
	}
	if worker.GetStatus().Attempt != 2 {
		t.Fatal("stale approval changed execution history")
	}
}

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
