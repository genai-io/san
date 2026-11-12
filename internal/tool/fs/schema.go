package fs

import (
	"fmt"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/proc"
	"github.com/genai-io/san/internal/tool"
)

// Schema returns the model-facing tool definition for Read. The limits and
// the truncation marker are formatted from the same constants read.go
// enforces, so the model's instructions can't drift from the behavior.
func (t *ReadTool) Schema() core.ToolSchema {
	return core.ToolSchema{
		Name: "Read",
		Description: fmt.Sprintf(`Reads a file from the local filesystem.

- Prefer relative paths for files inside the session working directory; absolute for targets outside it
- Reads up to %d lines from the start by default; when you already know which part of a large file you need, read just that part with offset/limit
- Read output has a line-number and tab prefix; strip it for Edit and preserve the rest exactly
- Lines over %d characters end with “%s” and cannot be copied into an Edit
- Do not re-read a file to verify your own Edit/Write — a failed change errors, and successful results keep your view current
- Image files are recognized but cannot be displayed yet; ask the user to attach the image to a message instead`, maxReadLines, maxLineLen, lineTruncationMarker),
		Definition: map[string]any{
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
		Description: `Performs exact string replacement in a file.

- Requires a current view: Read first, unless successful Write/Edit already observed the file this session. Re-read after external changes.
- old_string must match the file exactly after stripping Read's line-number prefix (preserve indentation) and must be unique — add surrounding context if not, or set replace_all to change every occurrence. Trailing-whitespace-only mismatches apply automatically; other whitespace slips fail with the actual lines echoed.
- Apply several changes to one file with multiple Edit calls in one message; they run in order.
- Do not Read and Edit the same file in one message.`,
		Definition: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{
					"type":        "string",
					"description": "Path to the file to modify. Relative paths are resolved from the current session working directory.",
				},
				"old_string": map[string]any{
					"type":        "string",
					"description": "The exact text to replace",
				},
				"new_string": map[string]any{
					"type":        "string",
					"description": "The replacement text (must differ from old_string)",
				},
				"replace_all": map[string]any{
					"type":        "boolean",
					"description": "Replace every occurrence of old_string (default false)",
				},
			},
			"required": []string{"file_path", "old_string", "new_string"},
		},
	}
}

// Schema returns the model-facing tool definition for Write.
func (t *WriteTool) Schema() core.ToolSchema {
	return core.ToolSchema{
		Name: "Write",
		Description: `Writes a file to the local filesystem, overwriting any existing content.

- To overwrite, Read the file first unless successful Write/Edit already observed it this session; re-read after external changes.
- Use Edit for every existing-file change; reserve Write for new files or wholesale regeneration.
- Never create documentation or README files unless explicitly requested.`,
		Definition: map[string]any{
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

var _ tool.ShellDirectoryAwareTool = (*ShellTool)(nil)

// Schema describes the selected interpreter with the host's default cwd behavior.
func (t *ShellTool) Schema() core.ToolSchema {
	return t.SchemaWithDirectoryHelp("")
}

// SchemaWithDirectoryHelp keeps host-specific guidance local to this schema.
func (t *ShellTool) SchemaWithDirectoryHelp(help string) core.ToolSchema {
	if help == "" {
		help = "A successful cd updates the session working directory"
	}
	name := t.Name()
	shell, err := t.resolve()
	if err != nil {
		shell = proc.Shell{Kind: proc.ShellBash}
	}
	description := fmt.Sprintf(unixShellDescription, shell, help)
	if name == tool.ToolPowerShell {
		description = fmt.Sprintf(powerShellDescription, shell, help)
	} else if shell.Kind == proc.ShellZsh {
		description += "\n\nUses zsh -f (no user startup files) and inherited environment. Complex/native expansions require human confirmation. Tool/rule name stays Bash."
	}
	// The second shell is only in play once the user turns it on, so the
	// division of labour is written there and never names a missing tool.
	if t.second {
		description += "\n\n" + secondShellNote(name)
	}
	description += "\n\nRun in the foreground by default. Use background_job only when requested or when you have an independent next step. Use background_service for persistent servers and verify readiness. Services stop when San exits, not on session changes; any self-exit (even code 0) is a failure."
	return core.ToolSchema{
		Name:        name,
		Description: description,
		Definition:  shellParameters(),
	}
}

const unixShellDescription = `Executes a %s command and returns its output.

- Starts in the session cwd; do not prepend cd to it. %s; other shell state does not persist.
- Use for discovery (rg/find/ls); bound large output with head/wc. Use Read/Edit/Write for file contents.
- No TTY or interactive stdin: use non-interactive flags or heredocs. Read-only commands may run without approval.
- Background processes detach; results include the process-group ID and stop commands.`

const powerShellDescription = `Executes a %s command and returns its output.

- Use PowerShell syntax: $env:NAME and backtick escaping. On 5.1, no && or ||; use ";" or "if ($?) { ... }".
- Starts in the session cwd; do not prepend Set-Location to it. %s; other shell state does not persist.
- Use for discovery (Get-ChildItem/Select-String/rg); limit output with Select-Object -First. Use Read/Edit/Write for file contents.
- No interactive console: use non-interactive flags. Simple read-only pipelines may run without approval.`

func shellParameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The command to execute",
			},
			"description": map[string]any{
				"type":        []string{"string", "null"},
				"description": "Short action label for approvals/tasks; omit or null to show the command.",
			},
			"timeout_ms": map[string]any{
				"type":        []string{"integer", "null"},
				"minimum":     1,
				"maximum":     maxShellTimeoutMS,
				"description": fmt.Sprintf("Execution deadline in ms (default %d, max %d), not a backgrounding threshold. Omit or null for background_service.", defaultShellTimeoutMS, maxShellTimeoutMS),
			},
			"execution_mode": map[string]any{
				"type":        []string{"string", "null"},
				"enum":        []any{shellForeground, shellBackgroundJob, shellBackgroundService, nil},
				"description": "foreground (default/null) waits for completion; background_job returns a task ID immediately; background_service runs a managed service without a deadline.",
			},
		},
		"required": []string{"command"},
	}
}

// secondShellNote divides work between the two Windows shells.
func secondShellNote(name string) string {
	if name == tool.ToolPowerShell {
		return "Bash (Git for Windows) is also available for POSIX tools/scripts; use PowerShell for Windows-specific work."
	}
	return "PowerShell is also available for Windows-specific work; use Bash for POSIX tools/scripts."
}
