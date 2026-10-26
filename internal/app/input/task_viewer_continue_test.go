package input

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/task"
)

func TestTaskViewerContinuationUsesStableTaskAndAttempt(t *testing.T) {
	for _, status := range []task.TaskStatus{task.StatusFailed, task.StatusCompleted, task.StatusStopped, task.StatusKilled, task.StatusRunning} {
		var viewer TaskViewer
		viewer.Show([]TaskViewItem{{ID: "stable", SessionID: "child", Attempt: 2, Status: status, CanContinue: true}}, 80, 24)
		viewer.HandleKeypress(taskKey("h"))
		viewID := viewer.ViewID()
		cmd := viewer.HandleKeypress(taskKey("c"))
		want := status == task.StatusFailed || status == task.StatusCompleted
		if (cmd != nil) != want {
			t.Fatalf("status %s: continuation available=%v", status, cmd != nil)
		}
		if want {
			msg := cmd().(TaskContinueMsg)
			if msg.TaskID != "stable" || msg.Attempt != 2 || msg.ViewID != viewID || !viewer.ActionPending() {
				t.Fatalf("lost continuation identity: %+v", msg)
			}
		}
	}
}

func TestTaskViewerContinuationHistoryAndRepeat(t *testing.T) {
	var viewer TaskViewer
	viewer.Show([]TaskViewItem{{ID: "stable", SessionID: "child", Attempt: 2, Attempts: "1 failed: provider unavailable", Status: task.StatusFailed, CanContinue: true}}, 40, 24)
	viewer.HandleKeypress(taskKey("h"))
	viewer.HandleKeypress(taskKey("enter"))
	view := ansi.Strip(viewer.Render())
	for _, want := range []string{"Session: child", "Attempt: 2", "provider unavailable", "c ask main to continue"} {
		if !strings.Contains(view, want) {
			t.Fatalf("lost %q: %s", want, view)
		}
	}
	if cmd := viewer.HandleKeypress(tea.KeyPressMsg{Code: 'c', Text: "c", IsRepeat: true}); cmd != nil {
		t.Fatal("key repeat requested duplicate continuation")
	}
}
