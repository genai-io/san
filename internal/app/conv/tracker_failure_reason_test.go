package conv

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/todo"
)

func TestFailedTrackerPrefersTerminalReasonOverActivity(t *testing.T) {
	const reason = "Bash permission denied: approval required"
	for _, live := range []bool{false, true} {
		item := &todo.Item{Subject: strings.Repeat("review long task ", 8), Status: todo.StatusCompleted,
			Metadata: map[string]any{"background_task_id": "worker", "background_status_detail": "failed", "background_error": reason}}
		params := TrackerListParams{Items: []*todo.Item{item}, Width: 100, CompletionShown: func(string) bool { return false }}
		if live {
			params.WorkerStatus = func(string) task.TaskInfo {
				return task.TaskInfo{Status: task.StatusFailed, Error: reason, Progress: "Read(stale.go)", OutputError: "log unavailable"}
			}
		}
		view := ansi.Strip(RenderTrackerList(params))
		if !strings.Contains(view, "[failed] · "+reason) || strings.Contains(view, "stale.go") {
			t.Fatalf("live=%v: failed row hid its reason: %q", live, view)
		}
		for _, width := range []int{20, 40, 80} {
			params.Width = width
			for line := range strings.SplitSeq(RenderTrackerList(params), "\n") {
				if ansi.StringWidth(line) > width {
					t.Fatalf("row overflows %d columns: %q", width, line)
				}
			}
		}
	}
}
