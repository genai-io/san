package input

import (
	"strings"
	"testing"

	"github.com/genai-io/san/internal/task"
)

func TestRecordedTaskHistoryIsAlwaysReadOnly(t *testing.T) {
	for _, status := range []task.TaskStatus{task.StatusRunning, task.StatusFailed, task.StatusCompleted} {
		var viewer TaskViewer
		viewer.Show([]TaskViewItem{{ID: "old", Status: status, Recorded: true, CanContinue: true, AwaitingApproval: true}}, 80, 24)
		viewer.HandleKeypress(taskKey("h"))
		viewer.HandleKeypress(taskKey("enter"))
		for _, key := range []string{"s", "r", "c"} {
			if cmd := viewer.HandleKeypress(taskKey(key)); cmd != nil || viewer.ActionPending() || viewer.confirm {
				t.Fatalf("recorded %s task allowed action %s", status, key)
			}
		}
		if hint := viewer.hint(); strings.Contains(hint, "s stop") || strings.Contains(hint, "r approval") || strings.Contains(hint, "c ask") {
			t.Fatalf("recorded task advertised unavailable actions: %s", hint)
		}
	}
}
