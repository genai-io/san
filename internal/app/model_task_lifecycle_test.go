package app

import (
	"context"
	"testing"

	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/todo"
)

// A background task's tracker item must exist before the task can finish.
//
// The item used to be built from the launching tool's result, which reaches
// the UI goroutine well after the task is already running. A task that ended
// first — a subagent rejected by its provider, a bash command that exits at
// once — completed an item that did not exist yet, so the completion was
// dropped and the item created afterwards named a task that had already
// ended. It stayed in_progress for the rest of the session: rendered
// [stalled] forever, and never letting the tracker panel reset.
//
// Registering the task is now what creates the item, and the manager
// announces that from inside RegisterTask — before the caller has a goroutine
// that could complete it — so the ordering holds by construction.
func TestWorkerItemSurvivesImmediateCompletion(t *testing.T) {
	todo.Initialize(todo.Options{})
	t.Cleanup(func() {
		todo.Default().Reset()
		task.SetLifecycleHandler(nil)
	})

	m := &model{services: services{Tracker: todo.Default()}}
	m.wireTaskLifecycle(nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	worker := task.NewAgentTask("bg-1", "Explore", "Audit deps", ctx, cancel)
	task.Default().RegisterTask(worker)
	worker.Complete(nil)

	items := todo.Default().List()
	if len(items) != 1 {
		t.Fatalf("expected 1 tracker item, got %d", len(items))
	}
	if items[0].Status != todo.StatusCompleted {
		t.Fatalf("status = %q, want %q — the item was stranded", items[0].Status, todo.StatusCompleted)
	}
}
