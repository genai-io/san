package tasktools

import (
	"context"
	"fmt"

	"github.com/genai-io/san/v2/internal/task/tracker"
	"github.com/genai-io/san/v2/internal/tool"
	"github.com/genai-io/san/v2/internal/tool/toolresult"
)

// TrackerCreateTool creates a new tracked task
type TrackerCreateTool struct{}

func (t *TrackerCreateTool) Name() string        { return "TaskCreate" }
func (t *TrackerCreateTool) Description() string { return "Create a task to track progress" }
func (t *TrackerCreateTool) Icon() string        { return "📋" }

func (t *TrackerCreateTool) Execute(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	subject := tool.GetString(params, "subject")
	if subject == "" {
		return toolresult.NewErrorResult(t.Name(), "subject is required")
	}

	description := tool.GetString(params, "description")
	if description == "" {
		return toolresult.NewErrorResult(t.Name(), "description is required")
	}

	activeForm := tool.GetString(params, "activeForm")
	metadata, _ := params["metadata"].(map[string]any)

	task := tracker.Default().Create(subject, description, activeForm, metadata)

	// Set dependencies if provided
	if ids := parseStringSlice(params["addBlockedBy"]); len(ids) > 0 {
		tracker.Default().Update(task.ID, tracker.WithAddBlockedBy(ids))
	}

	return toolresult.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("Task #%s created: %s", task.ID, task.Subject),
		Metadata: toolresult.ResultMetadata{
			Title:    t.Name(),
			Icon:     t.Icon(),
			Subtitle: task.Subject,
		},
	}
}

func init() {
	tool.Register(&TrackerCreateTool{})
}
