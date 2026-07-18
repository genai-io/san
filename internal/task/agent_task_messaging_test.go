package task

import (
	"context"
	"errors"
	"testing"
)

func newRunningAgentTask(t *testing.T, id string) *AgentTask {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return NewAgentTask(id, "Worker", "test task", ctx, cancel)
}

func TestAgentTaskCompleteDistinguishesStopFromFailure(t *testing.T) {
	stopped := newRunningAgentTask(t, "msg-2")
	stopped.Complete(context.Canceled)
	if info := stopped.GetStatus(); info.Status != StatusStopped {
		t.Fatalf("cancelled run status = %q, want stopped", info.Status)
	}

	wrapped := newRunningAgentTask(t, "msg-3")
	wrapped.Complete(errors.Join(errors.New("run aborted"), context.Canceled))
	if info := wrapped.GetStatus(); info.Status != StatusStopped {
		t.Fatalf("wrapped cancel status = %q, want stopped", info.Status)
	}

	failed := newRunningAgentTask(t, "msg-4")
	failed.Complete(errors.New("LLM completion failed"))
	if info := failed.GetStatus(); info.Status != StatusFailed {
		t.Fatalf("failure status = %q, want failed", info.Status)
	}
}
