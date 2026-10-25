package task

import (
	"context"
	"errors"
	"testing"
)

func TestRecoveryBudgetStopsRepeatedFailureChain(t *testing.T) {
	worker := NewAgentTask("budget", "worker", "task", context.Background(), func() {}, "")
	worker.Complete(errors.New("first failure"))
	attempt, err := worker.BeginAttempt("", context.Background(), func() {})
	if err != nil {
		t.Fatal(err)
	}
	worker.CompleteAttempt(attempt, errors.New("corrective execution failed"))
	if RecoveryBudgetAvailable(worker.GetStatus().Attempts) {
		t.Fatal("repeated failure chain retained an automatic recovery budget")
	}
	if _, err := worker.BeginAttempt("", context.Background(), func() {}); err == nil {
		t.Fatal("exhausted recovery began another execution")
	}
	if worker.GetStatus().Attempt != 2 {
		t.Fatal("rejected recovery changed attempt history")
	}
	if !RecoveryBudgetAvailable([]AttemptInfo{{Status: StatusFailed}, {Status: StatusCompleted}, {Status: StatusFailed}}) {
		t.Fatal("a later independent failure inherited an already completed chain")
	}
}

func TestManagerStopRemainsPinnedToSelectedAttempt(t *testing.T) {
	manager := NewManager()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := manager.CreateAgentTask("stable", "worker", "task", ctx, cancel)
	worker.Complete(errors.New("first failure"))
	next, stop := context.WithCancel(context.Background())
	defer stop()
	if _, err := worker.BeginAttempt("", next, stop); err != nil {
		t.Fatal(err)
	}
	if err := manager.KillAttempt(worker.GetID(), 1); err == nil {
		t.Fatal("stale manager stop accepted")
	}
	if next.Err() != nil || !worker.IsRunning() {
		t.Fatal("stale manager stop canceled the successor")
	}
	worker.CompleteAttempt(2, nil)
}
