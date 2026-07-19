package mode

import "github.com/genai-io/san/internal/core"

// Schema returns the model-facing tool definition for AskUserQuestion.
func (t *AskUserQuestionTool) Schema() core.ToolSchema {
	return core.ToolSchema{
		Name: "AskUserQuestion",
		Description: `Ask the user a question with predefined choices. An 'Other' option with free-text input is always appended automatically.

Single question (most common):
  {"question": "Which version?", "options": ["v1.0", "v2.0", "v3.0"]}

Multiple questions (rare):
  {"questions": [{"question": "Q1?", "options": ["A","B"]}, {"question": "Q2?", "options": ["X","Y"]}]}`,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"question": map[string]any{
					"type":        "string",
					"description": "The question text (for single question)",
				},
				"options": map[string]any{
					"type":        "array",
					"description": "2-8 short choice labels (for single question)",
					"minItems":    2,
					"maxItems":    8,
					"items":       map[string]any{"type": "string"},
				},
				"questions": map[string]any{
					"type":        "array",
					"description": "For multiple questions. Array of {question, options} objects (max 8).",
					"minItems":    1,
					"maxItems":    8,
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"question": map[string]any{"type": "string"},
							"options": map[string]any{
								"type":     "array",
								"minItems": 2,
								"maxItems": 8,
								"items":    map[string]any{"type": "string"},
							},
						},
						"required": []string{"question", "options"},
					},
				},
			},
			"minProperties": 1,
			"required":      []string{},
		},
	}
}

// Schema returns the model-facing tool definition for EnterWorktree.
func (t *EnterWorktreeTool) Schema() core.ToolSchema {
	return core.ToolSchema{
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
	}
}

// Schema returns the model-facing tool definition for ExitWorktree.
func (t *ExitWorktreeTool) Schema() core.ToolSchema {
	return core.ToolSchema{
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
	}
}
