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

// Schema returns the model-facing tool definition for Glob.
func (t *GlobTool) Schema() core.ToolSchema {
	return core.ToolSchema{
		Name: "Glob",
		Description: `Fast file pattern matching tool that works with any codebase size.
- Supports glob patterns like "**/*.go" or "src/**/*.ts"
- Returns matching file paths sorted by modification time (newest first)
- Use this tool when you need to find files by name patterns
- When you are doing an open-ended search that may require multiple rounds of globbing and grepping, use the Agent tool instead`,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "The glob pattern to match files against",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "The directory to search in. Defaults to the current session working directory. Prefer relative paths when searching inside it.",
				},
			},
			"required": []string{"pattern"},
		},
	}
}

// Schema returns the model-facing tool definition for Grep.
func (t *GrepTool) Schema() core.ToolSchema {
	return core.ToolSchema{
		Name: "Grep",
		Description: `A powerful search tool built on ripgrep

  Usage:
  - ALWAYS use Grep for search tasks. NEVER invoke grep or rg as a Bash command.
  - Supports full regex syntax (e.g., "log.*Error", "function\\s+\\w+")
  - Filter files with glob parameter (e.g., "*.js", "**/*.tsx") or type parameter (e.g., "js", "py", "rust")
  - Output modes: "content" shows matching lines, "files_with_matches" shows only file paths (default), "count" shows match counts
  - Use Agent tool for open-ended searches requiring multiple rounds
  - Pattern syntax: Uses ripgrep (not grep) - literal braces need escaping (use interface\{\} to find interface{} in Go code)
  - Multiline matching: By default patterns match within single lines only. For cross-line patterns, use multiline: true`,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "The regular expression pattern to search for in file contents",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "File or directory to search in (rg PATH). Defaults to the current session working directory. Prefer relative paths when searching inside it.",
				},
				"glob": map[string]any{
					"type":        "string",
					"description": "Glob pattern to filter files (e.g. \"*.js\", \"*.{ts,tsx}\") - maps to rg --glob",
				},
				"type": map[string]any{
					"type":        "string",
					"description": "File type to search (rg --type). Common types: js, py, rust, go, java, etc.",
				},
				"output_mode": map[string]any{
					"type":        "string",
					"enum":        []string{"content", "files_with_matches", "count"},
					"description": "Output mode: \"content\" shows matching lines, \"files_with_matches\" shows file paths (default), \"count\" shows match counts",
				},
				"-i": map[string]any{
					"type":        "boolean",
					"description": "Case insensitive search (rg -i). Default: true",
				},
				"-n": map[string]any{
					"type":        "boolean",
					"description": "Show line numbers in output (rg -n). Applies to content mode. Defaults to true.",
				},
				"context": map[string]any{
					"type":        "integer",
					"description": "Number of lines to show before and after each match (rg -C). Requires output_mode: \"content\".",
				},
				"-C": map[string]any{
					"type":        "integer",
					"description": "Alias for context.",
				},
				"-A": map[string]any{
					"type":        "integer",
					"description": "Number of lines to show after each match (rg -A). Requires output_mode: \"content\".",
				},
				"-B": map[string]any{
					"type":        "integer",
					"description": "Number of lines to show before each match (rg -B). Requires output_mode: \"content\".",
				},
				"multiline": map[string]any{
					"type":        "boolean",
					"description": "Enable multiline mode where . matches newlines and patterns can span lines (rg -U --multiline-dotall). Default: false.",
				},
				"head_limit": map[string]any{
					"type":        "integer",
					"description": "Limit output to first N lines/entries. Defaults to 250 when unspecified. Pass 0 for unlimited.",
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "Skip first N lines/entries before applying head_limit. Defaults to 0.",
				},
			},
			"required": []string{"pattern"},
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
- When editing text from Read tool output, ensure you preserve the exact indentation (tabs/spaces) as it appears AFTER the line number prefix. Never include any part of the line number prefix in the old_string or new_string.
- ALWAYS prefer editing existing files in the codebase. NEVER write new files unless explicitly required.
- Only use emojis if the user explicitly requests it. Avoid adding emojis to files unless asked.
- The edit will FAIL if an old_string is not unique in the file. Either provide a larger string with more surrounding context or use replace_all to change every instance of old_string.
- To make several independent replacements in one file, use edits: [{"old_string": "...", "new_string": "..."}]. All replacements are validated before the file is written.
- Use replace_all for replacing and renaming strings across a file.`,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{
					"type":        "string",
					"description": "Path to the file to modify. Relative paths are resolved from the current session working directory.",
				},
				"old_string": map[string]any{
					"type":        "string",
					"description": "The text to replace (must be different from new_string)",
				},
				"new_string": map[string]any{
					"type":        "string",
					"description": "The text to replace it with (must be different from old_string)",
				},
				"replace_all": map[string]any{
					"type":        "boolean",
					"description": "Replace all occurrences of old_string (default false)",
					"default":     false,
				},
				"edits": map[string]any{
					"type":        "array",
					"description": "Independent replacements to apply together. Cannot be combined with old_string or new_string.",
					"minItems":    1,
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"old_string":  map[string]any{"type": "string", "description": "Exact text to replace"},
							"new_string":  map[string]any{"type": "string", "description": "Replacement text"},
							"replace_all": map[string]any{"type": "boolean", "description": "Replace all occurrences (default false)", "default": false},
						},
						"required": []string{"old_string", "new_string"},
					},
				},
			},
			"required": []string{"file_path"},
			"oneOf": []any{
				map[string]any{"required": []string{"old_string", "new_string"}},
				map[string]any{"required": []string{"edits"}},
			},
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

IMPORTANT: Avoid using this tool to run grep, find, cat, head, tail, sed, awk, or echo commands. Instead, use the appropriate dedicated tool:
- File search: Use Glob (NOT find or ls)
- Content search: Use Grep (NOT grep or rg)
- Read files: Use Read (NOT cat/head/tail)
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
You can use the run_in_background parameter to run the command in the background. You will be notified when it finishes.`,
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
