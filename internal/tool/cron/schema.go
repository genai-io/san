package cron

import "github.com/genai-io/san/internal/core"

// Schema returns the model-facing tool definition for CronCreate.
func (t *CronCreateTool) Schema() core.ToolSchema {
	return core.ToolSchema{
		Name: "CronCreate",
		Description: `Schedule a prompt on a cron schedule. Uses standard 5-field cron: minute hour day-of-month month day-of-week.
Recurring jobs (default) auto-expire after 7 days. One-shot jobs (recurring=false) fire once then auto-delete.
Jobs only fire while the REPL is idle. Returns a job ID for CronDelete.`,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"cron": map[string]any{
					"type":        "string",
					"description": "5-field cron expression in local time (e.g., '*/5 * * * *', '0 9 * * 1-5')",
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "The prompt to enqueue at each fire time",
				},
				"recurring": map[string]any{
					"type":        "boolean",
					"description": "true (default) = fire repeatedly. false = fire once then auto-delete.",
				},
				"durable": map[string]any{
					"type":        "boolean",
					"description": "If true, job persists across sessions for this project (saved to .san/scheduled_tasks.json). Default: false (session-only).",
				},
			},
			"required": []string{"cron", "prompt"},
		},
	}
}

// Schema returns the model-facing tool definition for CronDelete.
func (t *CronDeleteTool) Schema() core.ToolSchema {
	return core.ToolSchema{
		Name:        "CronDelete",
		Description: "Cancel a scheduled cron job by its ID.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "The job ID returned by CronCreate",
				},
			},
			"required": []string{"id"},
		},
	}
}

// Schema returns the model-facing tool definition for CronList.
func (t *CronListTool) Schema() core.ToolSchema {
	return core.ToolSchema{
		Name:        "CronList",
		Description: "List all scheduled cron jobs with their status, next fire time, and prompt.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
}
