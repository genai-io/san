package fs

import "github.com/genai-io/san/internal/core"

// Schema returns the model-facing tool definition for Read.
func (t *ReadTool) Schema() core.ToolSchema {
	return core.ToolSchema{
		Name: "Read",
		Description: `Reads a file from the local filesystem. You can access any file directly by using this tool.
Assume this tool is able to read all files on the machine. If the User provides a path to a file assume that path is valid. It is okay to read a file that does not exist; an error will be returned.

Usage:
- The file_path parameter may be absolute or relative to the current session working directory
- Prefer relative paths for files inside the current session working directory; use absolute paths for files outside it
- By default, it reads up to 2000 lines starting from the beginning of the file
- You can optionally specify a line offset and limit (especially handy for long files), but it's recommended to read the whole file by not providing these parameters
- Results are returned with line numbers starting at 1
- This tool can only read files, not directories. To read a directory, use an ls command via the Bash tool.
- You will regularly be asked to read screenshots. If the user provides a path to a screenshot, ALWAYS use this tool to view the file at the path.
- If you read a file that exists but has empty contents you will receive a system reminder warning in place of file contents.`,
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
		Description: `Performs exact string replacements in files.

Usage:
- You must use your Read tool at least once in the conversation before editing. This tool will error if you attempt an edit without reading the file.
- If you need fresh file contents before editing, call Read and wait for its result before calling Edit. Do not call Read and Edit for the same target in the same assistant message.
- When editing text from Read tool output, ensure you preserve the exact indentation (tabs/spaces) as it appears AFTER the line number prefix. Never include any part of the line number prefix in the oldText or newText.
- ALWAYS prefer editing existing files in the codebase. NEVER write new files unless explicitly required.
- Only use emojis if the user explicitly requests it. Avoid adding emojis to files unless asked.
- Use edits to apply one or more independent replacements in a single file. Every oldText must match exactly once; include more surrounding text if it is not unique.
- All replacements are checked against the original file and applied together. Overlapping changes must be combined into one edit.`,
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
		Description: `Writes a file to the local filesystem.

Usage:
- This tool will overwrite the existing file if there is one at the provided path.
- If this is an existing file, you MUST use the Read tool first to read the file's contents. This tool will fail if you did not read the file first.
- Prefer the Edit tool for modifying existing files — it only sends the diff. Only use this tool to create new files or for complete rewrites.
- NEVER create documentation files (*.md) or README files unless explicitly requested by the User.
- Only use emojis if the user explicitly requests it. Avoid writing emojis to files unless asked.`,
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
		Description: `Executes a given bash command and returns its output.

CRITICAL — Working directory:
Commands already execute in the session working directory. NEVER prefix with
"cd <session-working-directory> &&". Use relative paths for files inside the
session working directory; reserve absolute paths for targets outside it.
A successful "cd" updates the session working directory for subsequent commands.
Shell state (variables, aliases) does not persist between calls.

Search and discovery run through this tool: use rg (preferred) or grep for
content search, find or fd for file discovery, and ls for listing. Pipe
through head/tail/wc to trim large output. Provably read-only commands
(search, listing, git inspection) run without approval prompts.

For file CONTENT operations, still use the dedicated tools:
- Read files: Use Read (NOT cat/head/tail) — returns line numbers, handles images
- Edit files: Use Edit (NOT sed/awk)
- Write files: Use Write (NOT echo/cat with redirection)

Non-interactive only:
Commands run with no controlling terminal and no stdin, so anything that waits
for interactive input — a REPL, an editor, a password/confirmation prompt —
cannot receive it and will hang until it times out. Pass a non-interactive flag
or supply input inline instead: use "git commit -m ..." (not a bare "git
commit"), "npm init -y", "ssh -o BatchMode=yes", "apt-get -y", or feed input via
a heredoc or a --stdin-style flag.

You may specify an optional timeout in milliseconds (up to 600000ms / 10 minutes). By default, your command will timeout after 120000ms (2 minutes).
You can use the run_in_background parameter to run the command in the background. You will be notified when it finishes. To cancel it early, call Agent with signal "stop" and its task ID.`,
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
