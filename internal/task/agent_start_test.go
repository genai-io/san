package task

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestStartAttemptPreparationDoesNotPublishOrOverlap(t *testing.T) {
	worker := NewAgentTask("start", "worker", "work", context.Background(), nil, "")
	worker.Complete(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failure := errors.New("setup failed")
	_, err := worker.StartAttemptAfter(1, "", ctx, cancel, func() error {
		if info := worker.GetStatus(); info.Attempt != 1 || info.Status != StatusCompleted {
			t.Errorf("preparation published an execution: %+v", info)
		}
		if _, err := worker.BeginAttempt("", context.Background(), nil); err == nil {
			t.Error("competing start was accepted")
		}
		return failure
	}, func() error {
		t.Error("failed preparation accepted input")
		return nil
	})
	if !errors.Is(err, failure) || worker.GetStatus().Attempt != 1 {
		t.Fatalf("failed preparation consumed an attempt: %v", err)
	}
	if _, err := worker.BeginAttempt("", ctx, cancel); err != nil {
		t.Fatalf("failed preparation retained its starting flag: %v", err)
	}
	worker.Complete(nil)
}

func TestStopDuringStartPreparationRejectsBeforeAcceptance(t *testing.T) {
	worker := NewAgentTask("stop-start", "worker", "work", context.Background(), nil, "")
	worker.Complete(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	done := make(chan error, 1)
	accepted := false
	go func() {
		_, err := worker.StartAttemptAfter(1, "", ctx, cancel, func() error {
			close(entered)
			<-ctx.Done()
			return nil
		}, func() error {
			accepted = true
			return nil
		})
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

func TestStartAcceptanceFailureDoesNotConsumeAttempt(t *testing.T) {
	worker := NewAgentTask("accept-failure", "worker", "work", context.Background(), nil, "")
	worker.Complete(nil)
	failure := errors.New("input rejected")
	if _, err := worker.StartAttemptAfter(1, "", context.Background(), nil, nil, func() error { return failure }); !errors.Is(err, failure) {
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

func TestStopAfterAcceptanceCancelsAdmittedAttempt(t *testing.T) {
	worker := NewAgentTask("commit-stop", "worker", "work", context.Background(), nil, "")
	worker.Complete(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	accepting, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := worker.StartAttemptAfter(1, "", ctx, cancel, nil, func() error {
			close(accepting)
			<-release
			return nil
		})
		done <- err
	}()
	<-accepting
	stopping, stopped := make(chan struct{}), make(chan error, 1)
	go func() {
		close(stopping)
		stopped <- worker.Stop()
	}()
	<-stopping
	close(release)
	if err := <-done; err != nil {
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
