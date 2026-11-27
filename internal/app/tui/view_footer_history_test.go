package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/todo"
)

func TestTaskHistorySharesInferenceRow(t *testing.T) {
	const width = 80
	m := modelWithInferenceProgress(24)
	m.conv.Stream.StartedAt = time.Now().Add(-17 * time.Second)
	before, _ := m.frameBottom()
	addPastTasks(m, task.StatusFailed, task.StatusFailed, task.StatusStopped, task.StatusKilled, task.StatusCompleted)
	const hint = "/tasks · 2 failed · 2 stopped"
	view := m.View()
	plain := ansi.Strip(view.Content)
	rows := strings.Split(plain, "\n")
	if len(rows) != 24 || view.Cursor == nil || view.Cursor.Position.Y != 21 {
		t.Fatalf("composer moved: rows=%d cursor=%+v", len(rows), view.Cursor)
	}
	inputRow := view.Cursor.Position.Y
	status := rows[inputRow-2]
	if !strings.HasPrefix(status, "✦ On it · ") || !strings.HasSuffix(status, hint) || ansi.StringWidth(status) != width-1 || rows[inputRow-1] != strings.Repeat("─", width-1) {
		t.Fatalf("history must share the inference row above the composer:\n%s", plain)
	}
	after, _ := m.frameBottom()
	if rowCount(after) != rowCount(before) || !strings.Contains(ansi.Strip(after), hint+"\n") || strings.Count(plain, "/tasks") != 1 || strings.Contains(plain, "Tasks ·") {
		t.Fatalf("history added a tracker row or changed the chat budget:\n%s", plain)
	}
	t.Logf("\n%s", strings.Join(rows[inputRow-2:inputRow+2], "\n"))
}

func addPastTasks(m *model, statuses ...task.TaskStatus) {
	m.conv.ShowTasks = true
	m.services.Task = task.NewRunSet()
	m.taskCompletionsShown = make(map[string]bool)
	for i, status := range statuses {
		worker := viewerWorker(m, fmt.Sprintf("old-%d", i), "", time.Now())
		todo.TrackWorker(m.services.Tracker, worker.info)
		worker.info.Status = status
		if status == task.StatusFailed {
			worker.info.Error = "past fixture error"
		}
		todo.CompleteWorker(m.services.Tracker, worker.info)
		m.taskCompletionsShown[taskCompletionKey(worker.GetID(), 0)] = true
	}
}

func TestTaskHistoryAdaptsToWidth(t *testing.T) {
	const status = "✦ On it · 17s"
	for _, tc := range []struct {
		width int
		hint  string
	}{
		{46, "/tasks · 2 failed · 2 stopped"},
		{45, "/tasks"},
		{24, "/tasks"},
		{21, ""},
	} {
		m := model{env: env{Width: tc.width}}
		row := ansi.Strip(m.renderFooterStatus(status, "2 failed · 2 stopped"))
		suffix, ok := strings.CutPrefix(row, status)
		if !ok || strings.TrimSpace(suffix) != tc.hint || suffix != "" && ansi.StringWidth(row) != tc.width-1 {
			t.Fatalf("width %d: got %q, want status with %q", tc.width, row, tc.hint)
		}
	}
}

func TestTaskHistoryStaysDiscoverableWhenIdle(t *testing.T) {
	m := fixedComposerModel(80, 12)
	addPastTasks(m, task.StatusStopped)
	frame, cursor := m.viewString()
	rows := strings.Split(ansi.Strip(frame), "\n")
	if cursor == nil || cursor.Position.Y != 9 || !strings.HasSuffix(rows[7], " /tasks · 1 stopped") || rows[8] != strings.Repeat("─", 79) || strings.Contains(frame, "Tasks ·") {
		t.Fatalf("idle history not attached to the composer:\n%s", frame)
	}
	m.conv.ShowTasks = false
	if frame, _ := m.viewString(); strings.Contains(frame, "/tasks") {
		t.Fatal("history shown with the tracker turned off")
	}
}

func TestFooterIntegratedHistoryQueueAndInferenceOrder(t *testing.T) {
	for _, width := range []int{100, 40, 32} {
		for _, height := range []int{24, 12, 8} {
			t.Run(fmt.Sprintf("%dx%d", width, height), func(t *testing.T) {
				m := modelWithInferenceProgress(height)
				m.env.Width = width
				addPastTasks(m, task.StatusFailed, task.StatusStopped, todo.StatusDetailInterrupted)
				live := viewerWorker(m, "live", "", time.Now())
				live.info.Description = "active-footer-worker"
				todo.TrackWorker(m.services.Tracker, live.info)
				m.userInput.Queue.Enqueue("pending-msg", nil)
				view := m.View()
				plain := ansi.Strip(view.Content)
				lines := strings.Split(plain, "\n")
				at := height - 5
				if strings.Contains(plain, "✦") || lines[at+1] != strings.Repeat("─", width-1) {
					t.Fatalf("visible live task should replace routine inference feedback:\n%s", plain)
				}
				wantHint := "/tasks"
				if width == 100 {
					wantHint += " · 1 failed · 1 stopped · 1 interrupted"
				}
				if !strings.HasSuffix(lines[at], wantHint) || strings.Count(plain, "/tasks") != 1 {
					t.Fatalf("history must appear once above the composer divider:\n%s", plain)
				}
				for _, label := range []string{"Tasks", "active-footer-worker", "pending-msg"} {
					if index := strings.Index(plain, label); index >= 0 && index >= strings.Index(plain, lines[at]) {
						t.Fatalf("%s placed below the composer rule:\n%s", label, plain)
					}
					if height == 24 && !strings.Contains(plain, label) {
						t.Fatalf("roomy integrated footer lost %s:\n%s", label, plain)
					}
				}
				pendingAt := strings.Index(plain, "pending-msg")
				if pendingAt < 0 {
					t.Fatalf("integrated footer lost pending message:\n%s", plain)
				}
				for _, label := range []string{"Tasks", "active-footer-worker"} {
					if index := strings.Index(plain, label); index >= 0 && index < pendingAt {
						t.Fatalf("pending message placed below %s:\n%s", label, plain)
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
