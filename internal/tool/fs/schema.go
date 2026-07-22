package fs

import "github.com/genai-io/san/internal/core"

// Schema returns the model-facing tool definition for Read.
func (t *ReadTool) Schema() core.ToolSchema {
	return core.ToolSchema{
		Name: "Read",
		Description: `Reads a file from the local filesystem.

- Prefer relative paths for files inside the session working directory; absolute for targets outside it
- Reads up to 2000 lines from the start by default; use offset/limit only for very long files
- Output is line-numbered starting at 1
- Images (e.g. screenshots) are supported — read the file to view it`,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{
					"type":        "string",
					"description": "Path to the file to read. Relative paths are resolved from the current session working directory.",
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "The line number to start reading from (1-based). Only provide if the file is too large to read at once.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "The number of lines to read. Only provide if the file is too large to read at once.",
				},
			},
			"required": []string{"file_path"},
		},
	}
}

// Schema returns the model-facing tool definition for Edit.
func (t *EditTool) Schema() core.ToolSchema {
	return core.ToolSchema{
		Name: "Edit",
		Description: `Performs exact string replacements in a file.

- Requires a prior Read of the file — editing an unread file errors. If you need fresh contents, Read and wait for the result before calling Edit; don't send both in the same message.
- oldText must match the file exactly (strip Read's line-number prefix, preserve indentation) and match exactly once — add surrounding context if it isn't unique.
- All edits are checked against the original file and applied together; combine overlapping changes into one edit.`,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the file to modify. Relative paths are resolved from the current session working directory.",
				},
				"edits": map[string]any{
					"type":        "array",
					"description": "One or more exact replacements applied together.",
					"minItems":    1,
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"oldText": map[string]any{"type": "string", "description": "Exact unique text to replace"},
							"newText": map[string]any{"type": "string", "description": "Replacement text"},
						},
						"required": []string{"oldText", "newText"},
					},
				},
			},
			"required": []string{"path", "edits"},
		},
	}
}

// Schema returns the model-facing tool definition for Write.
func (t *WriteTool) Schema() core.ToolSchema {
	return core.ToolSchema{
		Name: "Write",
		Description: `Writes a file to the local filesystem, overwriting any existing content.

- Overwriting an existing file requires a prior Read of it — the call fails otherwise.
- Prefer Edit for modifying existing files; use Write for new files or complete rewrites.
- Never create documentation or README files unless explicitly requested.`,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{
					"type":        "string",
					"description": "Path to the file to write. Relative paths are resolved from the current session working directory.",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "The content to write to the file",
				},
			},
			"required": []string{"file_path", "content"},
		},
	}
}

// Schema returns the model-facing tool definition for Bash.
func (t *BashTool) Schema() core.ToolSchema {
	return core.ToolSchema{
		Name: "Bash",
		Description: `Executes a bash command and returns its output.

- Commands already run in the session working directory — NEVER prefix with "cd <cwd> &&"; use relative paths inside it. A successful cd updates the session working directory; other shell state (variables, aliases) does not persist between calls.
- Search and discovery run through this tool (rg, find/fd, ls); pipe large output through head/wc. Provably read-only commands run without approval prompts.
- For file contents use the dedicated tools: Read (not cat), Edit (not sed), Write (not echo/redirection).
- No TTY and no stdin — anything awaiting interactive input hangs until timeout. Use non-interactive flags ("git commit -m", "npm init -y", "apt-get -y") or feed input via heredoc.
- Optional timeout in ms (default 120000, max 600000). run_in_background detaches the command; you are notified when it finishes, and can cancel it via Agent with signal "stop" and its task ID.`,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The command to execute",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Clear, concise description of what this command does in active voice",
				},
				"timeout": map[string]any{
					"type":        "integer",
					"description": "Optional timeout in milliseconds (max 600000)",
				},
				"run_in_background": map[string]any{
					"type":        "boolean",
					"description": "Set to true to run this command in the background. You will be notified when it completes.",
				},
			},
			"required": []string{"command"},
		},
	}
}
