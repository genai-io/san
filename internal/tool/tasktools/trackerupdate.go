package tasktools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/genai-io/san/internal/todo"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
)

// TrackerUpdateTool updates a task's status or details
type TrackerUpdateTool struct{}

func (t *TrackerUpdateTool) Name() string        { return "TaskUpdate" }
func (t *TrackerUpdateTool) Description() string { return "Update task status or details" }
func (t *TrackerUpdateTool) Icon() string        { return "📋" }

func (t *TrackerUpdateTool) Execute(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	taskID := tool.GetString(params, "taskId")
	if taskID == "" {
		return toolresult.NewErrorResult(t.Name(), "taskId is required")
	}

	// Handle deletion separately
	if status := tool.GetString(params, "status"); status == todo.StatusDeleted {
		if err := todo.Default().Delete(taskID); err != nil {
			return toolresult.NewErrorResult(t.Name(), err.Error())
		}
		return toolresult.ToolResult{
			Success: true,
			Output:  fmt.Sprintf("Task #%s deleted", taskID),
			Metadata: toolresult.ResultMetadata{
				Title:    t.Name(),
				Icon:     t.Icon(),
				Subtitle: fmt.Sprintf("#%s deleted", taskID),
			},
		}
	}

	opts, statusChange, err := buildUpdateOptions(params)
	if err != nil {
		return toolresult.NewErrorResult(t.Name(), err.Error())
	}

	if len(opts) == 0 {
		return toolresult.NewErrorResult(t.Name(), "no updates specified")
	}

	if err := todo.Default().Update(taskID, opts...); err != nil {
		return toolresult.NewErrorResult(t.Name(), err.Error())
	}

	subtitle := fmt.Sprintf("#%s", taskID)
	if statusChange != "" {
		subtitle += " " + statusChange
	}

	return toolresult.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("Updated task #%s", taskID),
		Metadata: toolresult.ResultMetadata{
			Title:    t.Name(),
			Icon:     t.Icon(),
			Subtitle: subtitle,
		},
	}
}

// buildUpdateOptions extracts update options from params, returns options, status change, and error
func buildUpdateOptions(params map[string]any) ([]todo.UpdateOption, string, error) {
	var opts []todo.UpdateOption
	var statusChange string

	if status := tool.GetString(params, "status"); status != "" {
		switch status {
		case todo.StatusPending, todo.StatusInProgress, todo.StatusCompleted:
			opts = append(opts, todo.WithStatus(status))
			statusChange = status
		default:
			return nil, "", fmt.Errorf("invalid status: %s (must be pending, in_progress, completed, or deleted)", status)
		}
	}

	if subject := tool.GetString(params, "subject"); subject != "" {
		opts = append(opts, todo.WithSubject(subject))
	}
	if description := tool.GetString(params, "description"); description != "" {
		opts = append(opts, todo.WithDescription(description))
	}
	if activeForm := tool.GetString(params, "activeForm"); activeForm != "" {
		opts = append(opts, todo.WithActiveForm(activeForm))
	}
	if owner := tool.GetString(params, "owner"); owner != "" {
		opts = append(opts, todo.WithOwner(owner))
	}
	if metadata, ok := params["metadata"].(map[string]any); ok {
		opts = append(opts, todo.WithMetadata(metadata))
	}
	if ids := parseStringSlice(params["addBlocks"]); len(ids) > 0 {
		opts = append(opts, todo.WithAddBlocks(ids))
	}
	if ids := parseStringSlice(params["addBlockedBy"]); len(ids) > 0 {
		opts = append(opts, todo.WithAddBlockedBy(ids))
	}

	return opts, statusChange, nil
}

// parseStringSlice converts an interface{} to []string, handling both
// []string and []interface{} (from JSON unmarshaling)
func parseStringSlice(v any) []string {
	switch val := v.(type) {
	case []string:
		return val
	case []any:
		var result []string
		for _, item := range val {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	case string:
		// Try to parse as JSON array
		var result []string
		if err := json.Unmarshal([]byte(val), &result); err == nil {
			return result
		}
		return []string{val}
	}
	return nil
}

func init() {
	tool.Register(&TrackerUpdateTool{})
}
