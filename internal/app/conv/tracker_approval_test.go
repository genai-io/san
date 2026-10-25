package conv

import (
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/todo"
)

func TestTrackerTerminalRowsRetireTransientActivity(t *testing.T) {
	for _, status := range []task.TaskStatus{task.StatusCompleted, task.StatusStopped, task.StatusFailed} {
		t.Run(string(status), func(t *testing.T) {
			store := todo.NewStore()
			info := task.TaskInfo{ID: "worker", Type: task.TaskTypeAgent, AgentName: "reviewer", Description: "Security review", Status: task.StatusRunning}
			todo.TrackWorker(store, info)
			info.Status, info.Progress = status, "Thinking..."
			if status == task.StatusStopped {
				info.Error = "stopped before completion"
			} else if status == task.StatusFailed {
				info.Error = "provider unavailable"
			}
			todo.CompleteWorker(store, info)
			view := stripANSI(RenderTrackerList(TrackerListParams{Items: store.List(), Width: 100, CompletionShown: func(string) bool { return false }, WorkerStatus: func(string) task.TaskInfo { return info }}))
			if strings.Contains(view, "Thinking...") || strings.Contains(view, "stopped before completion") {
				t.Fatalf("terminal row retained transient activity: %q", view)
			}
			if status == task.StatusFailed && !strings.Contains(view, "provider unavailable") {
				t.Fatalf("real failure reason was hidden: %q", view)
			}
		})
	}
}

func TestTrackerExplicitApprovalAndMixedPlan(t *testing.T) {
	todo.Initialize()
	t.Cleanup(func() { todo.Default().Reset() })
	worker := todo.Default().Create("Worker", "", "", map[string]any{"background_task_id": "bg", "background_status_detail": "running"})
	_ = todo.Default().Update(worker.ID, todo.WithStatus(todo.StatusInProgress))
	todo.Default().Create("Plan intent", "", "", nil)
	p := TrackerListParams{Items: todo.Default().List(), Width: 120, StreamActive: true, AwaitingApproval: map[string]bool{"bg": true}, WorkerStatus: func(string) task.TaskInfo { return task.TaskInfo{Status: task.StatusRunning} }}
	a := RenderTrackerList(p)
	p.Frame = 20
	time.Sleep(time.Millisecond)
	b := RenderTrackerList(p)
	if a != b || !strings.Contains(stripANSI(a), "awaiting approval") {
		t.Fatal("approval should stay still")
	}
	if !strings.Contains(a, "Plan intent") || !strings.Contains(a, "Background workers") || strings.Contains(a, "Queued") {
		t.Fatal("worker approval hid or confused the existing plan")
	}
	p.AwaitingApproval = nil
	if !strings.Contains(RenderTrackerList(p), "stalled") {
		t.Fatal("stale worker should remain stalled")
	}
	p.AwaitingApproval = map[string]bool{"bg": true}
	p.WorkerStatus = func(string) task.TaskInfo { return task.TaskInfo{Status: task.StatusStopped} }
	if strings.Contains(RenderTrackerList(p), "awaiting approval") {
		t.Fatal("terminal worker cannot await approval")
	}
}
