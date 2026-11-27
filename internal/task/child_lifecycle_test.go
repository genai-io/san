package task

import (
	"context"
	"testing"
)

func TestManagedChildLifecycleOwnership(t *testing.T) {
	observer := &testTaskObserver{}
	SetLifecycleHandler(observer)
	defer SetLifecycleHandler(nil)
	m := NewRunSet()
	ctx, cancel := context.WithCancel(WithSessionID(context.Background(), "session"))
	defer cancel()
	child := m.CreateChildAgentTask("child", "scheduler", "reviewer", "review", ctx, cancel)
	if len(observer.created) != 1 || len(observer.completed) != 0 {
		t.Fatalf("creation order: %+v", observer)
	}
	created := observer.created[0]
	if created.ID != "child" || created.ParentTaskID != "scheduler" || created.ParentSessionID != "session" || created.Status != StatusRunning {
		t.Fatalf("created ownership: %+v", created)
	}
	child.Complete(nil)
	if len(observer.completed) != 1 || observer.completed[0].ParentTaskID != "scheduler" || observer.completed[0].ParentSessionID != "session" {
		t.Fatalf("completion ownership: %+v", observer.completed)
	}
	foreground := m.CreateForegroundAgentTask("foreground", "reviewer", "normal", ctx, cancel)
	foreground.Complete(nil)
	if len(observer.created) != 1 || len(observer.completed) != 1 {
		t.Fatalf("foreground emitted lifecycle: %+v", observer)
	}
}
