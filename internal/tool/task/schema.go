package task

import "github.com/genai-io/san/internal/core"

// Schema returns the model-facing tool definition for TaskStop.
func (t *TaskStopTool) Schema() core.ToolSchema {
	return core.ToolSchema{
		Name:        "TaskStop",
		Description: "Stops a running background task by its ID. Returns a success or failure status. Use this tool when you need to terminate a long-running background agent or command.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "The ID of the background task to stop",
				},
			},
			"required": []string{"task_id"},
		},
	}
}

// Schema returns the model-facing tool definition for TaskOutput. The tool is
// intentionally not registered (see init), so this schema never reaches the
// model; it exists only to satisfy the tool.Tool interface.
func (t *TaskOutputTool) Schema() core.ToolSchema {
	return core.ToolSchema{
		Name:        "TaskOutput",
		Description: "[Deprecated] Inspect final result from a completed background task when the user explicitly asks. Background workers automatically notify you on completion — do not use this to poll or check progress.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "The ID of the background task to get output from",
				},
				"block": map[string]any{
					"type":        "boolean",
					"description": "If true, wait for task completion. If false (default), return current status/output immediately.",
					"default":     false,
				},
				"timeout": map[string]any{
					"type":        "integer",
					"description": "Maximum time to wait in milliseconds when block=true (default: 30000, max: 600000). Ignored for the default non-blocking mode.",
					"default":     30000,
				},
			},
			"required": []string{"task_id"},
		},
	}
}
