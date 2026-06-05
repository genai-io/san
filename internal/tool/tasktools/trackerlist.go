package tasktools

import (
	"context"
	"fmt"
	"strings"

	"github.com/genai-io/san/v2/internal/task/tracker"
	"github.com/genai-io/san/v2/internal/tool"
	"github.com/genai-io/san/v2/internal/tool/toolresult"
)

// TrackerListTool lists all tracked tasks
type TrackerListTool struct{}

func (t *TrackerListTool) Name() string        { return "TaskList" }
func (t *TrackerListTool) Description() string { return "List all tracked tasks" }
func (t *TrackerListTool) Icon() string        { return "📋" }

func (t *TrackerListTool) Execute(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	// Reload from disk to pick up changes from other processes
	tracker.Default().ReloadFromDisk()

	tasks := tracker.Default().List()

	if len(tasks) == 0 {
		return toolresult.ToolResult{
			Success: true,
			Output:  "No tasks found.",
			Metadata: toolresult.ResultMetadata{
				Title:    t.Name(),
				Icon:     t.Icon(),
				Subtitle: "0 tasks",
			},
		}
	}

	// Build compact output: one line per task with ID, status, owner.
	// Subject is omitted — the full task list is visible in the tracker panel.
	// LLM can use TaskGet(taskId) for full details.
	var sb strings.Builder
	completed := 0
	for _, task := range tasks {
		if task.Status == tracker.StatusCompleted {
			completed++
		}
		line := fmt.Sprintf("#%s [%s]", task.ID, task.Status)
		if task.Owner != "" {
			line += fmt.Sprintf(" owner:%s", task.Owner)
		}
		sb.WriteString(line + "\n")
	}

	subtitle := fmt.Sprintf("%d/%d done", completed, len(tasks))

	return toolresult.ToolResult{
		Success: true,
		Output:  sb.String(),
		Metadata: toolresult.ResultMetadata{
			Title:    t.Name(),
			Icon:     t.Icon(),
			Subtitle: subtitle,
		},
	}
}

// TaskIcon returns the status icon for a task.
func TaskIcon(task *tracker.Task) string {
	switch task.Status {
	case tracker.StatusCompleted:
		return "✓"
	case tracker.StatusInProgress:
		return "⠋"
	default:
		if tracker.Default().IsBlocked(task.ID) {
			return "▸"
		}
		return "☐"
	}
}

func init() {
	tool.Register(&TrackerListTool{})
}
