package tasktools

import (
	"context"
	"fmt"
	"strings"

	"github.com/genai-io/san/internal/task/tracker"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
)

// TrackerGetTool retrieves a task by ID
type TrackerGetTool struct{}

func (t *TrackerGetTool) Name() string        { return "TaskGet" }
func (t *TrackerGetTool) Description() string { return "Retrieve task details by ID" }
func (t *TrackerGetTool) Icon() string        { return "📋" }

func (t *TrackerGetTool) Execute(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	taskID := tool.GetString(params, "taskId")
	if taskID == "" {
		return toolresult.NewErrorResult(t.Name(), "taskId is required")
	}

	// Reload from disk to pick up changes from other processes
	tracker.Default().ReloadFromDisk()

	task, ok := tracker.Default().Get(taskID)
	if !ok {
		// Fallback: background agent tasks use hex IDs from the task manager,
		// stored as "background_task_id" metadata in tracker entries.
		task = tracker.Default().FindByMetadata("background_task_id", taskID)
		if task == nil {
			return toolresult.NewErrorResult(t.Name(), fmt.Sprintf("task %s not found", taskID))
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Task #%s: %s\n", task.ID, task.Subject)
	fmt.Fprintf(&sb, "Status: %s\n", task.Status)
	if task.Description != "" {
		fmt.Fprintf(&sb, "Description: %s\n", task.Description)
	}
	if task.ActiveForm != "" {
		fmt.Fprintf(&sb, "Active form: %s\n", task.ActiveForm)
	}
	if task.Owner != "" {
		fmt.Fprintf(&sb, "Owner: %s\n", task.Owner)
	}
	if len(task.Blocks) > 0 {
		fmt.Fprintf(&sb, "Blocks: %s\n", strings.Join(task.Blocks, ", "))
	}
	if openBlockers := tracker.Default().OpenBlockers(task.ID); len(openBlockers) > 0 {
		fmt.Fprintf(&sb, "Blocked by (open): %s\n", strings.Join(openBlockers, ", "))
	}

	return toolresult.ToolResult{
		Success: true,
		Output:  sb.String(),
		Metadata: toolresult.ResultMetadata{
			Title:    t.Name(),
			Icon:     t.Icon(),
			Subtitle: fmt.Sprintf("#%s %s", task.ID, task.Subject),
		},
	}
}

func init() {
	tool.Register(&TrackerGetTool{})
}
