package task

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBeginAttemptWithSetupPreparationDoesNotPublishOrOverlap(t *testing.T) {
	worker := NewAgentTask("start", "worker", "work", context.Background(), nil, "")
	worker.Complete(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failure := errors.New("setup failed")
	_, err := worker.BeginAttemptWithSetup(ctx, cancel, 1, "", AttemptSetup{Prepare: func() error {
		if info := worker.GetStatus(); info.Attempt != 1 || info.Status != StatusCompleted {
			t.Errorf("preparation published an execution: %+v", info)
		}
		if _, err := worker.BeginAttempt("", context.Background(), nil); err == nil {
			t.Error("competing start was accepted")
		}
		return failure
	}, Accept: func() error {
		t.Error("failed preparation accepted input")
		return nil
	}})
	if !errors.Is(err, failure) || worker.GetStatus().Attempt != 1 {
		t.Fatalf("failed preparation consumed an attempt: %v", err)
	}
	if _, err := worker.BeginAttempt("", ctx, cancel); err != nil {
		t.Fatalf("failed preparation retained its starting flag: %v", err)
	}
	worker.Complete(nil)
}

func TestBeginAttemptWithSetupStopDuringPreparationRejects(t *testing.T) {
	worker := NewAgentTask("stop-start", "worker", "work", context.Background(), nil, "")
	worker.Complete(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	done := make(chan error, 1)
	accepted := false
	go func() {
		_, err := worker.BeginAttemptWithSetup(ctx, cancel, 1, "", AttemptSetup{Prepare: func() error {
			close(entered)
			<-ctx.Done()
			return nil
		}, Accept: func() error {
			accepted = true
			return nil
		}})
		done <- err
	}()
	<-entered
	if err := worker.Stop(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil || accepted {
			t.Fatalf("stopped preparation accepted input: accepted=%v err=%v", accepted, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stop did not cancel preparation")
	}
	if info := worker.GetStatus(); info.Attempt != 1 || info.Status != StatusStopped || len(info.Attempts) != 1 {
		t.Fatalf("stopped preparation changed attempt history: %+v", info)
	}
}

func TestBeginAttemptWithSetupAcceptFailureConsumesNoAttempt(t *testing.T) {
	worker := NewAgentTask("accept-failure", "worker", "work", context.Background(), nil, "")
	worker.Complete(nil)
	failure := errors.New("input rejected")
	if _, err := worker.BeginAttemptWithSetup(context.Background(), nil, 1, "", AttemptSetup{Accept: func() error { return failure }}); !errors.Is(err, failure) {
		t.Fatalf("acceptance error lost: %v", err)
	}
	if info := worker.GetStatus(); info.Attempt != 1 || info.Status != StatusCompleted {
		t.Fatalf("rejected input published an execution: %+v", info)
	}
	if _, err := worker.BeginAttempt("", context.Background(), nil); err != nil {
		t.Fatalf("rejected acceptance retained startup ownership: %v", err)
	}
	worker.Complete(nil)
}

func TestBeginAttemptWithSetupStopDuringAcceptCancelsAttempt(t *testing.T) {
	worker := NewAgentTask("commit-stop", "worker", "work", context.Background(), nil, "")
	worker.Complete(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopped := make(chan error, 1)
	_, err := worker.BeginAttemptWithSetup(ctx, cancel, 1, "", AttemptSetup{Accept: func() error {
		go func() { stopped <- worker.Stop() }()
		waitForStopBlockedOnTaskLock(t)
		return nil
	}})
	if err != nil {
		t.Fatalf("accepted input was reported rejected: %v", err)
	}
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	worker.CompleteAttempt(2, nil)
	if info := worker.GetStatus(); info.Attempt != 2 || info.Status != StatusStopped || ctx.Err() == nil {
		t.Fatalf("stop did not cancel the admitted attempt: %+v", info)
	}
}

// waitForStopBlockedOnTaskLock waits until a Stop call is parked on the task
// lock held by Accept, so the test covers Stop racing acceptance rather than
// running after publication.
func waitForStopBlockedOnTaskLock(t *testing.T) {
	t.Helper()
	buf := make([]byte, 1<<20)
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		n := runtime.Stack(buf, true)
		for _, g := range strings.Split(string(buf[:n]), "\n\n") {
			if strings.Contains(g, "sync.(*RWMutex).") && strings.Contains(g, "task.(*AgentTask).Stop(") {
				return
			}
		}
	}
	t.Fatal("Stop never blocked on the task lock")
}
