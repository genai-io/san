package tool

import "github.com/genai-io/san/v2/internal/core"

// trackerToolSchemas defines the schemas for task management tools.
var trackerToolSchemas = []core.ToolSchema{
	{
		Name: "TaskCreate",
		Description: `Create a task to track progress on multi-step work.

When to use:
- Complex tasks requiring 3+ distinct steps
- User provides multiple tasks at once

When NOT to use:
- Single straightforward task or trivial fix
- Purely conversational or informational exchange

Granularity: one task per logical deliverable (a file, a feature, a test suite).
Don't create tasks for sub-steps within a single file or for "planning"/"summarizing".

Tips:
- Prefer sending ALL TaskCreate calls in a single message (parallel tool calls) for speed
- Use imperative subjects ("Fix bug", "Add tests")
- Provide activeForm for spinner display ("Fixing bug", "Adding tests")
- Check TaskList first to avoid duplicates
- Task IDs are sequential integers starting from 1. Use addBlockedBy to set dependencies (e.g. addBlockedBy=["1"])`,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"subject": map[string]any{
					"type":        "string",
					"description": "A brief, actionable title in imperative form (e.g., 'Fix authentication bug')",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Detailed description of what needs to be done, including context and acceptance criteria",
				},
				"activeForm": map[string]any{
					"type":        "string",
					"description": "Present continuous form shown in spinner when in_progress (e.g., 'Fixing authentication bug'). If omitted, the spinner shows the subject instead.",
				},
				"metadata": map[string]any{
					"type":        "object",
					"description": "Arbitrary metadata to attach to the task",
				},
				"addBlockedBy": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Task IDs that must complete before this task can start",
				},
			},
			"required": []string{"subject", "description"},
		},
	},
	{
		Name: "TaskGet",
		Description: `Retrieve full task details by ID (description, status, dependencies).

When to use:
- Before starting work on a task — get full requirements
- To check task dependencies (what it blocks, what blocks it)

Tip: Verify blockedBy is empty before beginning work.`,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"taskId": map[string]any{
					"type":        "string",
					"description": "The ID of the task to retrieve",
				},
			},
			"required": []string{"taskId"},
		},
	},
	{
		Name: "TaskUpdate",
		Description: `Update a task's status, details, or dependencies.

Status: pending → in_progress → completed. Use "deleted" to remove.
- Set in_progress BEFORE starting work
- ONLY mark completed when FULLY done (not if tests fail or partial)
- After completing, call TaskList for next task
- If blocked, keep as in_progress and create a new task for the blocker
- When you see a <task-reminder>, review and update stale tasks immediately`,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"taskId": map[string]any{
					"type":        "string",
					"description": "The ID of the task to update",
				},
				"status": map[string]any{
					"type":        "string",
					"description": "New status: pending, in_progress, completed, or deleted",
				},
				"subject": map[string]any{
					"type":        "string",
					"description": "New subject for the task",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "New description for the task",
				},
				"activeForm": map[string]any{
					"type":        "string",
					"description": "Present continuous form shown in spinner when in_progress (e.g., 'Fixing authentication bug')",
				},
				"owner": map[string]any{
					"type":        "string",
					"description": "New owner for the task (agent name)",
				},
				"metadata": map[string]any{
					"type":        "object",
					"description": "Metadata keys to merge into the task (set a key to null to delete it)",
				},
				"addBlocks": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Task IDs that this task blocks",
				},
				"addBlockedBy": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Task IDs that block this task",
				},
			},
			"required": []string{"taskId"},
		},
	},
	{
		Name: "TaskList",
		Description: `List all tasks with their status and dependencies.

When to use:
- Check overall progress
- After completing a task — find next available work
- Find blocked tasks that need dependencies resolved

Returns summary per task: id, status, owner. Use TaskGet for full details.
Prefer working on tasks in ID order (lowest first).`,
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	},
}

// cronToolSchemas defines the schemas for cron/scheduler tools.
var cronToolSchemas = []core.ToolSchema{
	{
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
	},
	{
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
	},
	{
		Name:        "CronList",
		Description: "List all scheduled cron jobs with their status, next fire time, and prompt.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	},
}

// worktreeToolSchemas defines the schemas for git worktree tools.
var worktreeToolSchemas = []core.ToolSchema{
	{
		Name: "EnterWorktree",
		Description: `Switch the current conversation into a git worktree for safe experimentation.
Creates an isolated copy of the repository where you can make changes without affecting the main working tree.
Use ExitWorktree to return to the original directory when done.`,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Optional slug for the worktree directory name (letters, digits, dots, underscores, dashes; max 64 chars)",
				},
			},
		},
	},
	{
		Name: "ExitWorktree",
		Description: `Exit the current worktree session and return to the original working directory.
Use action "keep" to preserve the worktree for later, or "remove" (default) to clean it up.
If removing with uncommitted changes, set discard_changes=true.`,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "What to do with the worktree: 'keep' (preserve for later) or 'remove' (clean up). Default: 'remove'.",
					"enum":        []string{"keep", "remove"},
				},
				"discard_changes": map[string]any{
					"type":        "boolean",
					"description": "If true, discard uncommitted changes when removing. Required when action='remove' and changes exist.",
				},
			},
		},
	},
}
