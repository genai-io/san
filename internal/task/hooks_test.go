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

// A killed task is just as terminal as a finished one. It used to skip the
// notification entirely, which stranded its tracker entry in in_progress —
// and a stranded entry kept the UI animating long after the turn ended.
func TestKillNotifiesLifecycleHandler(t *testing.T) {
	observer := &testTaskObserver{}
	SetLifecycleHandler(observer)
	defer SetLifecycleHandler(nil)

	mgr := NewManager()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start command: %v", err)
	}
	task := mgr.CreateBashTask(cmd, "sleep 30", "Long task", ctx, cancel)

	_ = task.Kill()

	if len(observer.completed) != 1 {
		t.Fatalf("expected 1 completed notification after kill, got %d", len(observer.completed))
	}
	if observer.completed[0].Status != StatusKilled {
		t.Fatalf("expected killed status, got %q", observer.completed[0].Status)
	}
	if task.IsRunning() {
		t.Fatal("killed task still reports running")
	}

	// The terminal transition is idempotent: a Complete racing in behind the
	// kill must not emit a second, contradictory notification.
	task.Complete(0, nil)
	if len(observer.completed) != 1 {
		t.Fatalf("terminal transition not idempotent: got %d notifications", len(observer.completed))
	}
}
