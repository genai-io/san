package app

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/todo"
)

func TestTaskHistoryRecordedFailuresRemainInspectable(t *testing.T) {
	m := interactionModel()
	m.services.sessionID = "current"
	info := task.TaskInfo{ID: "previous-process", Type: task.TaskTypeBash, Description: "Previous validation", Status: task.StatusRunning}
	todo.TrackWorker(m.services.Tracker, info)
	info.Status, info.Error = task.StatusFailed, "exit status 2: invalid fixture"
	todo.CompleteWorker(m.services.Tracker, info)
	items := m.taskSnapshots()
	if len(items) != 1 || !items[0].Recorded || items[0].Status != task.StatusFailed || items[0].Error != info.Error || items[0].CanContinue || items[0].Output != "" || items[0].OutputFile != "" {
		t.Fatalf("recorded history lost evidence or fabricated live state: %+v", items)
	}
	m.openTasks()
	plain := ansi.Strip(m.userInput.Tasks.Render())
	if !strings.Contains(plain, "History · 1 · 1 failed") || strings.Contains(plain, "Previous validation") {
		t.Fatalf("recorded failure did not start collapsed:\n%s", plain)
	}
	m.userInput.Tasks.HandleKeypress(tea.KeyPressMsg{Code: 'h', Text: "h"})
	m.userInput.Tasks.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEnter})
	plain = ansi.Strip(m.userInput.Tasks.Render())
	for _, want := range []string{"Previous validation", info.Error, "Recorded task; live worker unavailable.", "No recent output available."} {
		if !strings.Contains(plain, want) {
			t.Fatalf("recorded history hid %q:\n%s", want, plain)
		}
	}
	for _, key := range []rune{'s', 'r', 'c'} {
		if cmd := m.userInput.Tasks.HandleKeypress(tea.KeyPressMsg{Code: key, Text: string(key)}); cmd != nil || m.userInput.Tasks.ActionPending() {
			t.Fatalf("recorded history allowed live action %q", key)
		}
	}
	if item := m.services.Tracker.List()[0]; todo.BackgroundError(item) != info.Error || todo.BackgroundStatusDetail(item) != "failed" {
		t.Fatal("browsing history changed the persisted failure")
	}
	m.services.Task = nil
	if items = m.taskSnapshots(); len(items) != 1 || !items[0].Recorded {
		t.Fatal("recorded history depends on a live manager")
	}
}

func TestTaskHistoryLiveWorkerOverridesRecordedEntry(t *testing.T) {
	m := interactionModel()
	m.services.sessionID = "current"
	live := viewerWorker(m, "same", "current", time.Now())
	live.info.OutputFile = "/tmp/live-task-output.log"
	todo.TrackWorker(m.services.Tracker, live.info)
	old := live.info
	old.Status, old.Error = task.StatusFailed, "old failure"
	todo.CompleteWorker(m.services.Tracker, old)
	foreign := viewerWorker(m, "foreign", "other", time.Now())
	todo.TrackWorker(m.services.Tracker, foreign.info)
	old = foreign.info
	old.Status = task.StatusFailed
	todo.CompleteWorker(m.services.Tracker, old)
	plan := m.services.Tracker.Create("Unrelated plan", "", "", nil)
	_ = m.services.Tracker.Update(plan.ID, todo.WithStatus(todo.StatusCompleted))
	items := m.taskSnapshots()
	if len(items) != 1 || items[0].ID != "same" || items[0].Recorded || items[0].Status != task.StatusRunning || items[0].OutputFile != live.info.OutputFile {
		t.Fatalf("history duplicated a worker, leaked foreign history, or replaced live state: %+v", items)
	}
}

func TestTaskHistoryRecordedInterruptedWorkerIsNotFailure(t *testing.T) {
	m := interactionModel()
	m.services.Tracker.Create("Interrupted review", "", "", map[string]any{
		"background_task_id": "old-review", "background_status_detail": todo.StatusDetailInterrupted,
	})
	item := m.services.Tracker.List()[0]
	_ = m.services.Tracker.Update(item.ID, todo.WithStatus(todo.StatusCompleted))
	m.openTasks()
	plain := ansi.Strip(m.userInput.Tasks.Render())
	if !strings.Contains(plain, "History · 1") || strings.Contains(plain, "failed") || strings.Contains(plain, "Interrupted review") {
		t.Fatalf("interrupted history was misclassified:\n%s", plain)
	}
	m.userInput.Tasks.HandleKeypress(tea.KeyPressMsg{Code: 'h', Text: "h"})
	if plain = ansi.Strip(m.userInput.Tasks.Render()); !strings.Contains(plain, "[interrupted]") {
		t.Fatalf("history lost the interrupted status:\n%s", plain)
	}
}
