package task

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAgentAttemptContinuation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := NewAgentTask("same", "worker", "work", ctx, cancel, "")
	a.parentSessionID = "parent"
	if _, err := a.BeginAttempt("parent", ctx, cancel); err == nil {
		t.Fatal("overlap accepted")
	}
	a.AppendOutput([]byte("useful work"))
	a.Complete(errors.New("provider failed"))
	if _, err := a.BeginAttempt("other", ctx, cancel); err == nil {
		t.Fatal("wrong parent accepted")
	}
	n, err := a.BeginAttempt("parent", ctx, cancel)
	if err != nil {
		t.Fatal(err)
	}
	a.CompleteAttempt(1, nil)
	if !a.IsRunning() {
		t.Fatal("stale callback ended new execution")
	}
	if a.WaitForCompletion(time.Millisecond) {
		t.Fatal("new done channel already closed")
	}
	a.AppendOutput([]byte("follow-up"))
	a.CompleteAttempt(n, nil)
	info := a.GetStatus()
	if info.Attempt != 2 || len(info.Attempts) != 2 || info.Attempts[0].Error != "provider failed" || info.Output != "follow-up" {
		t.Fatalf("lost attempt history: %+v", info)
	}
	if err := a.Kill(); err != nil {
		t.Fatal(err)
	}
}

func TestAttemptOutputFailureIsIndependent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := NewAgentTask("output-health", "", "", ctx, cancel, "")
	a.outputErr = "disk unavailable"
	a.CompleteAttempt(1, nil)
	info := a.GetStatus()
	if info.Status != StatusCompleted || info.Error != "" || info.OutputError == "" {
		t.Fatalf("output health changed execution outcome: %+v", info)
	}
}

func TestRetiredAttemptCannotStopSuccessor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := NewAgentTask("scoped", "", "", ctx, cancel, "")
	a.CompleteAttempt(1, nil)
	nextCtx, nextCancel := context.WithCancel(context.Background())
	defer nextCancel()
	n, err := a.BeginAttempt("", nextCtx, nextCancel)
	if err != nil {
		t.Fatal(err)
	}
	if a.StopAttempt(1) == nil || a.KillAttempt(1) == nil {
		t.Fatal("stale stop accepted")
	}
	if nextCtx.Err() != nil || !a.IsRunning() {
		t.Fatal("stale stop affected successor")
	}
	if !a.WaitForAttemptCompletion(1, time.Millisecond) || a.WaitForAttemptCompletion(n, time.Millisecond) {
		t.Fatal("wait selected wrong generation")
	}
	a.CompleteAttempt(n, nil)
}

func TestAgentKilledAttemptCannotResume(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := NewAgentTask("id", "", "", ctx, cancel, "")
	_ = a.Kill()
	if _, err := a.BeginAttempt("", ctx, cancel); err == nil {
		t.Fatal("killed task resumed")
	}
}
