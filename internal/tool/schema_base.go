package tool

import "github.com/genai-io/san/internal/core"

var readToolSchema = core.ToolSchema{
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

var globToolSchema = core.ToolSchema{
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

var grepToolSchema = core.ToolSchema{
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

var webFetchToolSchema = core.ToolSchema{
	Name: "WebFetch",
	Description: `Fetches content from a specified URL and converts HTML to Markdown for readability.

Usage notes:
- The URL must be a fully-formed valid URL
- HTTP URLs will be automatically upgraded to HTTPS
- This tool is read-only and does not modify any files
- Results may be truncated if the content is very large
- For GitHub URLs, prefer using the gh CLI via Bash instead (e.g., gh pr view, gh issue view, gh api)`,
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "The URL to fetch content from",
			},
			"format": map[string]any{
				"type":        "string",
				"description": "Output format: 'markdown' (default) or 'raw'",
			},
		},
		"required": []string{"url"},
	},
}

var webSearchToolSchema = core.ToolSchema{
	Name: "WebSearch",
	Description: `Search the web for up-to-date information. Returns a list of relevant results with titles, URLs, and snippets.
When searching for current information, always use the present year rather than previous years.`,
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "The search query",
			},
			"num_results": map[string]any{
				"type":        "integer",
				"description": "Number of results to return (default: 10)",
			},
			"allowed_domains": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Only include results from these domains",
			},
			"blocked_domains": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Exclude results from these domains",
			},
		},
		"required": []string{"query"},
	},
}

var editToolSchema = core.ToolSchema{
	Name: "Edit",
	Description: `Performs exact string replacements in files.

Usage:
- You must use your Read tool at least once in the conversation before editing. This tool will error if you attempt an edit without reading the file.
- If you need fresh file contents before editing, call Read and wait for its result before calling Edit. Do not call Read and Edit for the same target in the same assistant message.
- When editing text from Read tool output, ensure you preserve the exact indentation (tabs/spaces) as it appears AFTER the line number prefix. Never include any part of the line number prefix in the old_string or new_string.
- ALWAYS prefer editing existing files in the codebase. NEVER write new files unless explicitly required.
- Only use emojis if the user explicitly requests it. Avoid adding emojis to files unless asked.
- The edit will FAIL if old_string is not unique in the file. Either provide a larger string with more surrounding context to make it unique or use replace_all to change every instance of old_string.
- Use replace_all for replacing and renaming strings across the file. This parameter is useful if you want to rename a variable for instance.`,
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
		},
		"required": []string{"file_path", "old_string", "new_string"},
	},
}

var writeToolSchema = core.ToolSchema{
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

var bashToolSchema = core.ToolSchema{
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

var taskOutputToolSchema = core.ToolSchema{
	Name:        ToolTaskOutput,
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

var taskStopToolSchema = core.ToolSchema{
	Name:        ToolTaskStop,
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

var askUserQuestionToolSchema = core.ToolSchema{
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

func baseToolSchemas() []core.ToolSchema {
	return []core.ToolSchema{
		readToolSchema,
		globToolSchema,
		grepToolSchema,
		webFetchToolSchema,
		webSearchToolSchema,
		editToolSchema,
		writeToolSchema,
		bashToolSchema,
		taskStopToolSchema,
		askUserQuestionToolSchema,
	}
}
