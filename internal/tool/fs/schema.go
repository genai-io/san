package fs

import (
	"fmt"

	"github.com/genai-io/san/internal/core"
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

// Schema returns the model-facing tool definition: Bash, or PowerShell where
// that is the shell. The parameters are the same; the guidance is per shell.
func (t *ShellTool) Schema() core.ToolSchema {
	name := t.Name()
	description := bashDescription
	if name == tool.ToolPowerShell {
		shell, _ := t.resolve()
		description = fmt.Sprintf(powerShellDescription, shell)
	}
	// The second shell is only in play once the user turns it on, so the
	// division of labour is written there and never names a missing tool.
	if t.second {
		description += "\n\n" + secondShellNote(name)
	}
	description += "\n\nRun commands in the foreground by default. Use execution_mode='background_job' when explicitly requested or while doing a concrete independent next step; use 'background_service' for a server that must keep running. If your next step needs the result, wait for it; do not background routine short commands. timeout_ms is an execution deadline, not a wait-before-background threshold. Services have no execution deadline: omit timeout_ms or use null. They are managed by this San process and stopped on application shutdown, not session switching. A service exiting on its own, even with exit code 0, is a failure. Check readiness before relying on a service; launching it is not a health check."
	return core.ToolSchema{
		Name:        name,
		Description: description,
		Definition:  shellParameters(),
	}
}

const bashDescription = `Executes a bash command and returns its output.

- Commands already run in the session working directory — NEVER prefix with "cd <cwd> &&"; use relative paths inside it. A successful cd updates the session working directory; other shell state (variables, aliases) does not persist between calls.
- Search and discovery run through this tool (rg, find/fd, ls); pipe large output through head/wc. Provably read-only commands run without approval prompts.
- For file contents use the dedicated tools: Read (not cat), Edit (not sed), Write (not echo/redirection).
- No TTY and no stdin — anything awaiting interactive input hangs until timeout. Use non-interactive flags ("git commit -m", "npm init -y", "apt-get -y") or feed input via heredoc.
- On Unix, background execution detaches the command; its result includes the process-group ID and exact Bash commands for graceful or forced termination.`

const powerShellDescription = `Executes a PowerShell command and returns its output. The shell is %s.

- Write PowerShell, not bash: separate statements with ";" (Windows PowerShell 5.1 has no && or ||; use "if ($?) { ... }"), send errors away with 2>$null, read environment variables as $env:NAME, and escape with a backtick.
- Commands already run in the session working directory — NEVER prefix with "Set-Location <cwd>;"; use relative paths inside it. A successful Set-Location (cd) updates the session working directory; other state (variables, functions) does not persist between calls.
- Search and discovery run through this tool (Get-ChildItem -Recurse -Filter, Select-String, or rg when installed); cut large output with Select-Object -First N. A single pipeline of read-only cmdlets runs without an approval prompt; anything with ; && ( { $ or a redirection asks.
- For file contents use the dedicated tools: Read (not Get-Content), Edit (not -replace), Write (not Set-Content/Out-File).
- No console and no stdin — anything awaiting input hangs until timeout. Use non-interactive flags (-Force, -Confirm:$false, "git commit -m").
- Background execution runs the command detached; you are notified when it completes.`

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
				"description": "Optional short description in active voice (e.g. Run the unit tests), shown in approvals and task notices. Omit or use null to show a command preview instead.",
			},
			"timeout_ms": map[string]any{
				"type":        []string{"integer", "null"},
				"minimum":     1,
				"maximum":     maxShellTimeoutMS,
				"description": fmt.Sprintf("Execution deadline in milliseconds for foreground or background_job (default %d, maximum %d). Omit or use null for the default; background_service requires omission or null. Does not control when a call returns or becomes background work.", defaultShellTimeoutMS, maxShellTimeoutMS),
			},
			"execution_mode": map[string]any{
				"type":        []string{"string", "null"},
				"enum":        []any{shellForeground, shellBackgroundJob, shellBackgroundService, nil},
				"description": "Defaults to foreground (also when null): wait and return the result directly. background_job returns a task ID immediately for finite work with an independent next step. background_service starts a managed long-running service without an execution deadline; verify readiness separately.",
			},
		},
		"required": []string{"command"},
	}
}

// secondShellNote tells the model how to split work when both shells are on.
func secondShellNote(name string) string {
	if name == tool.ToolPowerShell {
		return "The Bash tool (Git for Windows) is also available: keep scripts and POSIX tools (grep, sed, make) in Bash, and use this tool for Windows-specific work — cmdlets, the registry, services, Windows paths."
	}
	return "The PowerShell tool is also available: use this tool for scripts and POSIX tools (grep, sed, make), and PowerShell for Windows-specific work — cmdlets, the registry, services."
}
