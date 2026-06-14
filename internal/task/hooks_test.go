package task

import (
	"context"
	"os/exec"
	"testing"
)

type testTaskObserver struct {
	created   []TaskInfo
	completed []TaskInfo
}

func (o *testTaskObserver) TaskCreated(info TaskInfo) {
	o.created = append(o.created, info)
}

func (o *testTaskObserver) TaskCompleted(info TaskInfo) {
	o.completed = append(o.completed, info)
}

func TestTaskLifecycleHandler(t *testing.T) {
	observer := &testTaskObserver{}
	SetLifecycleHandler(observer)
	defer SetLifecycleHandler(nil)

	mgr := NewManager()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "echo", "test")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start command: %v", err)
	}

	task := mgr.CreateBashTask(cmd, "echo test", "Test task", ctx, cancel)
	if len(observer.created) != 1 {
		t.Fatalf("expected 1 created notification, got %d", len(observer.created))
	}
	if observer.created[0].ID != task.ID {
		t.Fatalf("expected created task id %q, got %q", task.ID, observer.created[0].ID)
	}

	task.Complete(0, nil)
	if len(observer.completed) != 1 {
		t.Fatalf("expected 1 completed notification, got %d", len(observer.completed))
	}
	if observer.completed[0].Status != StatusCompleted {
		t.Fatalf("expected completed status, got %q", observer.completed[0].Status)
	}
}
