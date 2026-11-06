package task

import (
	"context"
	"testing"
)

func TestIdleIncludesWorkerCleanup(t *testing.T) {
	pool := NewManager()
	if !pool.Idle() {
		t.Fatal("new pool is busy")
	}
	worker := pool.CreateAgentTask("worker", "test", "test", context.Background(), func() {})
	if pool.Idle() {
		t.Fatal("registered launch is idle")
	}
	release := make(chan struct{})
	defer pool.Join()
	defer close(release)
	pool.Run(func() { <-release })
	worker.Complete(nil)
	if pool.Idle() {
		t.Fatal("terminal worker with pending cleanup is idle")
	}
	release <- struct{}{}
	pool.Join()
	if !pool.Idle() {
		t.Fatal("joined pool is busy")
	}
}
