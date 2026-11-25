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
			rows, _ := RenderTrackerList(TrackerListParams{Items: store.List(), Width: 100, CompletionShown: func(string) bool { return false }, WorkerStatus: func(string) task.TaskInfo { return info }})
			view := stripANSI(rows)
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
	tracker := todo.NewStore()
	worker := tracker.Create("Worker", "", "", map[string]any{"background_task_id": "bg", "background_status_detail": "running"})
	_ = tracker.Update(worker.ID, todo.WithStatus(todo.StatusInProgress))
	tracker.Create("Plan intent", "", "", nil)
	p := TrackerListParams{Items: tracker.List(), Width: 120, StreamActive: true, AwaitingApproval: map[string]bool{"bg": true}, WorkerStatus: func(string) task.TaskInfo {
		return task.TaskInfo{Status: task.StatusRunning, Progress: "Preparing test command"}
	}}
	a, _ := RenderTrackerList(p)
	p.Frame = 20
	b, _ := RenderTrackerList(p)
	if a != b || !strings.Contains(stripANSI(a), "\n ○ Worker · Awaiting approval\n") || strings.Contains(stripANSI(a), "Preparing test command") {
		t.Fatalf("approval should stay still and replace the last activity:\n%s", a)
	}
	if !strings.Contains(a, "Plan intent") || !strings.Contains(a, "Background workers") || strings.Contains(a, "Queued") {
		t.Fatal("worker approval hid or confused the existing plan")
	}
	p.AwaitingApproval = nil
	if rows, _ := RenderTrackerList(p); !strings.Contains(rows, "stalled") || strings.Contains(rows, "Preparing test command") {
		t.Fatal("stalled worker should stay still and omit its last activity")
	}
	p.AwaitingApproval = map[string]bool{"bg": true}
	p.WorkerStatus = func(string) task.TaskInfo { return task.TaskInfo{Status: task.StatusStopped} }
	if rows, _ := RenderTrackerList(p); strings.Contains(rows, "Awaiting approval") {
		t.Fatal("terminal worker cannot await approval")
	}
}

func TestTrackerRunningActivityPrecedesElapsedTime(t *testing.T) {
	item := &todo.Item{
		Subject: "Review deploy logs", Status: todo.StatusInProgress,
		Metadata: map[string]any{"background_task_id": "worker"},
	}
	params := TrackerListParams{
		Items: []*todo.Item{item}, Width: 80,
		Executing: func(*todo.Item) bool { return true },
		WorkerStatus: func(string) task.TaskInfo {
			return task.TaskInfo{Status: task.StatusRunning, Progress: "Reading deploy.log"}
		},
	}
	for _, tc := range []struct {
		width     int
		age       time.Duration
		wantStart string
		wantClock string
	}{
		{80, 50 * time.Second, " ● Review deploy logs · Reading deploy.log · ", ""},
		{42, 50 * time.Second, " ● Review deploy logs · Reading", ""},
		{42, 70 * time.Second, " ● Review deploy logs · Reading", "1m "},
		{30, 70 * time.Second, "● Review deploy logs · 1m ", "1m "},
	} {
		params.Width = tc.width
		item.StatusChangedAt = time.Now().Add(-tc.age)
		rows, _ := RenderTrackerList(params)
		lines := strings.Split(strings.TrimSuffix(stripANSI(rows), "\n"), "\n")
		if len(lines) != 2 || !strings.HasPrefix(lines[1], tc.wantStart) {
			t.Fatalf("width %d age %s: expected one worker row starting with %q:\n%s", tc.width, tc.age, tc.wantStart, rows)
		}
		parts := strings.Split(lines[1], " · ")
		clock := parts[len(parts)-1]
		if !strings.HasPrefix(clock, tc.wantClock) || !strings.HasSuffix(clock, "s") {
			t.Fatalf("width %d age %s: timer missing at end: %q", tc.width, tc.age, lines[1])
		}
	}
}
