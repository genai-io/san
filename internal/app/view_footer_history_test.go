package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/todo"
)

func TestHistoryOnlyFooterSitsAgainstInference(t *testing.T) {
	m := modelWithInferenceProgress(24)
	m.env.Width = 80
	m.conv.ShowTasks = true
	m.services.Task = task.NewManager()
	worker := viewerWorker(m, "old", "", time.Now())
	todo.TrackWorker(m.services.Tracker, worker.info)
	worker.info.Status = task.StatusStopped
	todo.CompleteWorker(m.services.Tracker, worker.info)
	m.taskCompletionsShown = map[string]bool{taskCompletionKey(worker.GetID(), 0): true}
	view := m.View()
	plain := ansi.Strip(view.Content)
	rows := strings.Split(plain, "\n")
	for i, row := range rows {
		if strings.Contains(row, "Tasks · 1 past task") {
			if i+2 >= len(rows) || !strings.Contains(rows[i+1], "On it") || !strings.HasPrefix(rows[i+2], "─") {
				t.Fatalf("history should sit directly above inference and input:\n%s", plain)
			}
			if len(rows) != 24 || view.Cursor == nil || view.Cursor.Position.Y != 21 {
				t.Fatalf("composer moved: rows=%d cursor=%+v", len(rows), view.Cursor)
			}
			return
		}
	}
	t.Fatalf("missing history summary:\n%s", plain)
}

func TestFooterIntegratedHistoryQueueAndInferenceOrder(t *testing.T) {
	for _, width := range []int{100, 40, 32} {
		for _, height := range []int{24, 12, 8} {
			t.Run(fmt.Sprintf("%dx%d", width, height), func(t *testing.T) {
				m := modelWithInferenceProgress(height)
				m.env.Width = width
				m.conv.ShowTasks = true
				m.services.Task = task.NewManager()
				m.taskCompletionsShown = make(map[string]bool)
				for i := range 12 {
					worker := viewerWorker(m, fmt.Sprintf("old-%d", i), "", time.Now())
					todo.TrackWorker(m.services.Tracker, worker.info)
					worker.info.Status, worker.info.Error = task.StatusFailed, "past fixture error"
					todo.CompleteWorker(m.services.Tracker, worker.info)
					m.taskCompletionsShown[taskCompletionKey(worker.GetID(), 0)] = true
				}
				live := viewerWorker(m, "live", "", time.Now())
				live.info.Description = "active-footer-worker"
				todo.TrackWorker(m.services.Tracker, live.info)
				m.userInput.Queue.Enqueue("pending-msg", nil)
				view := m.View()
				plain := ansi.Strip(view.Content)
				lines := strings.Split(plain, "\n")
				at := -1
				for i, line := range lines {
					if strings.Contains(line, "✦ On it") {
						at = i
					}
				}
				if strings.Count(plain, "✦") != 1 || at < 0 || at+1 >= len(lines) || !strings.HasPrefix(lines[at+1], "─") {
					t.Fatalf("inference not single and adjacent to input:\n%s", plain)
				}
				for _, label := range []string{"Tasks", "/tasks", "active-footer-worker", "pending-msg"} {
					if index := strings.Index(plain, label); index >= 0 && index > strings.Index(plain, "✦ On it") {
						t.Fatalf("%s placed below inference:\n%s", label, plain)
					}
					if height == 24 && !strings.Contains(plain, label) {
						t.Fatalf("roomy integrated footer lost %s:\n%s", label, plain)
					}
				}
				if strings.Contains(plain, "past fixture error") || strings.Contains(plain, "subject-old") {
					t.Fatalf("historical failure rows still crowd the live footer:\n%s", plain)
				}
				if len(lines) != height || view.Cursor == nil || view.Cursor.Position.Y != height-3 {
					t.Fatalf("footer changed composer budget: rows=%d cursor=%+v", len(lines), view.Cursor)
				}
			})
		}
	}
}
