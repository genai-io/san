package input

import "testing"

func TestTaskViewerStopConfirmationPinsAttempt(t *testing.T) {
	var viewer TaskViewer
	item := TaskViewItem{ID: "stable", Status: "running", Attempt: 3}
	viewer.Show([]TaskViewItem{item}, 80, 24)
	viewID := viewer.ViewID()
	viewer.HandleKeypress(taskKey("s"))
	viewer.HandleKeypress(taskKey("tab"))
	cmd := viewer.HandleKeypress(taskKey("enter"))
	if cmd == nil {
		t.Fatal("stop confirmation produced no action")
	}
	msg := cmd().(TaskStopMsg)
	if msg.TaskID != item.ID || msg.Attempt != 3 || msg.ViewID != viewID {
		t.Fatalf("stop lost its confirmed execution identity: %+v", msg)
	}
}

func TestTaskViewerReplacementAttemptCancelsStopConfirmation(t *testing.T) {
	var viewer TaskViewer
	item := TaskViewItem{ID: "stable", Status: "running", Attempt: 1}
	viewer.Show([]TaskViewItem{item}, 80, 24)
	viewer.HandleKeypress(taskKey("s"))
	viewer.HandleKeypress(taskKey("tab"))
	item.Attempt = 2
	viewer.Refresh([]TaskViewItem{item})
	if viewer.confirm || viewer.confirmStop {
		t.Fatal("stop confirmation survived a replacement execution")
	}
	if cmd := viewer.HandleKeypress(taskKey("enter")); cmd != nil || viewer.ActionPending() {
		t.Fatal("retired confirmation emitted a stop for the new execution")
	}
}
