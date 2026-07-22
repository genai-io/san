package tasktools

import (
	"context"
	"fmt"
	"strings"

	"github.com/genai-io/san/internal/todo"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
)

// TrackerGetTool reads tasks: a single task's full details when given a taskId,
// or a summary of every task when the taskId is omitted.
type TrackerGetTool struct{}

func (t *TrackerGetTool) Name() string        { return "TaskGet" }
func (t *TrackerGetTool) Description() string { return "Read a task's details, or list all tasks" }
func (t *TrackerGetTool) Icon() string        { return "📋" }

func (t *TrackerGetTool) Execute(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	// Reload from disk to pick up changes from other processes (background agents).
	todo.Default().ReloadFromDisk()

	// No taskId means "list every task" — the compact overview.
	taskID := tool.GetString(params, "taskId")
	if taskID == "" {
		return t.listAll()
	}

	task, ok := todo.Default().Get(taskID)
	if !ok {
		// Fallback: background agent tasks use hex IDs from the task manager,
		// stored as "background_task_id" metadata in tracker entries.
		task = todo.Default().FindByMetadata("background_task_id", taskID)
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
	if openBlockers := todo.Default().OpenBlockers(task.ID); len(openBlockers) > 0 {
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

// listAll returns a compact one-line-per-task overview (id, status, owner).
// The subject is omitted — the full list is visible in the tracker panel, and
// TaskGet(taskId) fetches a single task's details.
func (t *TrackerGetTool) listAll() toolresult.ToolResult {
	tasks := todo.Default().List()
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

	var sb strings.Builder
	completed := 0
	for _, task := range tasks {
		if task.Status == todo.StatusCompleted {
			completed++
		}
		line := fmt.Sprintf("#%s [%s]", task.ID, task.Status)
		if task.Owner != "" {
			line += fmt.Sprintf(" owner:%s", task.Owner)
		}
		sb.WriteString(line + "\n")
	}

	return toolresult.ToolResult{
		Success: true,
		Output:  sb.String(),
		Metadata: toolresult.ResultMetadata{
			Title:    t.Name(),
			Icon:     t.Icon(),
			Subtitle: fmt.Sprintf("%d/%d done", completed, len(tasks)),
		},
	}
}

func init() {
	tool.Register(&TrackerGetTool{})
}
