package fs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/genai-io/san/internal/proc"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/internal/tool/toolresult"
)

const (
	IconBash      = "$"
	cwdFileEnvVar = "SAN_CWD_FILE"
)

// BashTool executes shell commands
type BashTool struct{}

func (t *BashTool) Name() string        { return "Bash" }
func (t *BashTool) Description() string { return "Execute shell commands" }
func (t *BashTool) Icon() string        { return IconBash }

// RequiresPermission returns true - Bash always requires permission
func (t *BashTool) RequiresPermission() bool {
	return true
}

// PreparePermission prepares a permission request with command preview
func (t *BashTool) PreparePermission(ctx context.Context, params map[string]any, cwd string) (*perm.PermissionRequest, error) {
	command, err := tool.RequireString(params, "command")
	if err != nil {
		return nil, err
	}

	description := tool.GetString(params, "description")
	runBackground := tool.GetBool(params, "run_in_background")

	// Count lines in command
	lineCount := strings.Count(command, "\n") + 1

	return &perm.PermissionRequest{
		ID:          tool.GenerateRequestID(),
		ToolName:    t.Name(),
		Description: description,
		BashMeta: &perm.BashMetadata{
			Command:       command,
			Description:   description,
			RunBackground: runBackground,
			LineCount:     lineCount,
		},
	}, nil
}

// ExecuteApproved executes the command after user approval
func (t *BashTool) ExecuteApproved(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	start := time.Now()

	command := tool.GetString(params, "command")
	description := tool.GetString(params, "description")
	runBackground := tool.GetBool(params, "run_in_background")

	// Get timeout (default 120 seconds, max 600 seconds)
	timeout := min(time.Duration(tool.GetFloat64(params, "timeout", 120000))*time.Millisecond, 600*time.Second)

	// Handle background execution
	if runBackground {
		return t.executeBackground(ctx, command, description, cwd, timeout)
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	trackedCommand, trackedFile, cleanup := prepareCwdTracking(command)
	defer cleanup()

	// Execute command
	cmd := exec.CommandContext(ctx, "bash", "-c", trackedCommand)
	cmd.Dir = cwd
	cmd.Env = bashEnv(ctx)
	if trackedFile != "" {
		cmd.Env = append(cmd.Env, cwdFileEnvVar+"="+trackedFile)
	}

	if responder := tool.PromptResponderFromContext(ctx); responder != nil {
		fullOutput, err := runInteractive(ctx, command, cmd, responder)
		duration := time.Since(start)
		return t.foregroundResult(ctx, description, fullOutput, "", err, duration, timeout, trackedFile, cwd)
	}

	// Detach from the controlling terminal so an interactive command grabs no
	// tty and fails fast instead of hanging the TUI. On timeout/cancel, tear
	// down the whole process group (not just bash) so a child that grabbed the
	// terminal — an editor, ssh — can't outlive the deadline; WaitDelay is the
	// backstop for a grandchild that inherited the output pipe and won't close.
	proc.DetachSession(cmd)
	cmd.Cancel = func() error {
		_ = proc.TerminateGroup(cmd, syscall.SIGKILL)
		return nil
	}
	cmd.WaitDelay = 5 * time.Second

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	duration := time.Since(start)

	// A command that backgrounds a child inheriting the output pipe (e.g.
	// "./server &") exits 0, but WaitDelay then fires because that child keeps
	// the pipe open. bash itself finished cleanly, so treat it as success
	// rather than surfacing the "WaitDelay expired" error as a failure.
	if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.Success() {
		err = nil
	}

	output := stdout.String()
	errOutput := stderr.String()

	return t.foregroundResult(ctx, description, output, errOutput, err, duration, timeout, trackedFile, cwd)
}

func (t *BashTool) foregroundResult(ctx context.Context, description, output, errOutput string, err error, duration time.Duration, timeout time.Duration, trackedFile, cwd string) toolresult.ToolResult {
	fullOutput := output
	if errOutput != "" {
		if fullOutput != "" {
			fullOutput += "\n"
		}
		fullOutput += errOutput
	}

	// Count lines
	lineCount := 0
	if fullOutput != "" {
		lineCount = strings.Count(strings.TrimSuffix(fullOutput, "\n"), "\n") + 1
	}

	// Truncate if too long
	const maxLen = 30000
	truncated := false
	if len(fullOutput) > maxLen {
		fullOutput = fullOutput[:maxLen] + "\n... (output truncated)"
		truncated = true
	}

	// Build CC-compatible structured response for hooks
	hookResponse := map[string]any{
		"stdout":           output,
		"stderr":           errOutput,
		"interrupted":      ctx.Err() == context.DeadlineExceeded,
		"isImage":          false,
		"noOutputExpected": false,
	}
	if newCwd := readTrackedCwd(trackedFile, cwd); newCwd != "" {
		hookResponse["cwd"] = newCwd
	}

	if err != nil {
		// Check if it's a timeout
		if ctx.Err() == context.DeadlineExceeded {
			return toolresult.ToolResult{
				Success: false,
				Output:  fullOutput,
				Error: "command timed out after " + timeout.String() +
					" — if it was waiting for interactive input, re-run with a non-interactive flag (e.g. -y, -m, BatchMode=yes) or supply input inline",
				HookResponse: hookResponse,
				Metadata: toolresult.ResultMetadata{
					Title:     t.Name(),
					Icon:      t.Icon(),
					Subtitle:  "Timeout",
					LineCount: lineCount,
					Duration:  duration,
				},
			}
		}

		// Command failed
		errorMsg := err.Error()
		if exitErr, ok := err.(*exec.ExitError); ok {
			errorMsg = fmt.Sprintf("exit code %d", exitErr.ExitCode())
		}

		return toolresult.ToolResult{
			Success:      false,
			Output:       fullOutput,
			Error:        errorMsg,
			HookResponse: hookResponse,
			Metadata: toolresult.ResultMetadata{
				Title:     t.Name(),
				Icon:      t.Icon(),
				Subtitle:  "Failed: " + errorMsg,
				LineCount: lineCount,
				Duration:  duration,
			},
		}
	}

	// Build subtitle
	subtitle := "Done"
	if description != "" {
		subtitle = description
	} else if truncated {
		subtitle = fmt.Sprintf("%d+ lines (truncated)", lineCount)
	} else if lineCount > 1 {
		subtitle = fmt.Sprintf("%d lines", lineCount)
	} else if output != "" {
		// Show first line preview for single-line output
		firstLine := strings.TrimSpace(strings.Split(output, "\n")[0])
		if len(firstLine) > 50 {
			firstLine = firstLine[:50] + "..."
		}
		if firstLine != "" {
			subtitle = firstLine
		}
	}

	return toolresult.ToolResult{
		Success:      true,
		Output:       fullOutput,
		HookResponse: hookResponse,
		Metadata: toolresult.ResultMetadata{
			Title:     t.Name(),
			Icon:      t.Icon(),
			Subtitle:  subtitle,
			LineCount: lineCount,
			Duration:  duration,
		},
	}
}

// Execute implements the Tool interface (for permission-unaware execution)
func (t *BashTool) Execute(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	// This will be called if permission flow is bypassed
	return t.ExecuteApproved(ctx, params, cwd)
}

// executeBackground runs the command in the background and returns immediately
func (t *BashTool) executeBackground(ctx context.Context, command, description, cwd string, timeout time.Duration) toolresult.ToolResult {
	// Create context with timeout for background task
	taskCtx, cancel := context.WithTimeout(context.Background(), timeout)

	// Create command
	cmd := exec.CommandContext(taskCtx, "bash", "-c", command)
	cmd.Dir = cwd
	cmd.Env = bashEnv(ctx)

	// Start the child in its own session: detached from the controlling
	// terminal (so an interactive command fails fast instead of grabbing the
	// tty) and the leader of a new process group, so cancellation can take down
	// any descendants it spawns (Unix). No-op on Windows.
	proc.DetachSession(cmd)
	// Override the default exec.CommandContext cancel, which only kills the
	// direct child via Process.Kill — without this, context-cancel of the
	// background task leaves any grandchildren the bash shell spawned alive.
	cmd.Cancel = func() error {
		_ = proc.TerminateGroup(cmd, syscall.SIGKILL)
		return nil
	}
	// Give exec a backstop so cmd.Wait can't hang on a grandchild that has
	// inherited the stdout/stderr pipe and refuses to close it.
	cmd.WaitDelay = 5 * time.Second

	// Set up pipes for stdout and stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return toolresult.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to create stdout pipe: %v", err),
			Metadata: toolresult.ResultMetadata{
				Title: t.Name(),
				Icon:  t.Icon(),
			},
		}
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdout.Close()
		cancel()
		return toolresult.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to create stderr pipe: %v", err),
			Metadata: toolresult.ResultMetadata{
				Title: t.Name(),
				Icon:  t.Icon(),
			},
		}
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		cancel()
		return toolresult.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to start command: %v", err),
			Metadata: toolresult.ResultMetadata{
				Title: t.Name(),
				Icon:  t.Icon(),
			},
		}
	}

	// Register with task manager
	bgTask := task.Default().CreateBashTask(cmd, command, description, taskCtx, cancel)

	// Start goroutine to collect output and wait for completion
	go func() {
		defer cancel()

		// Read stdout and stderr concurrently
		var stdoutBuf, stderrBuf bytes.Buffer
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = io.Copy(&stdoutBuf, stdout)
		}()
		go func() {
			defer wg.Done()
			_, _ = io.Copy(&stderrBuf, stderr)
		}()

		// Wait for command to complete, then wait for pipe drains
		err := cmd.Wait()
		wg.Wait()

		// Combine output
		output := stdoutBuf.String()
		if stderrBuf.Len() > 0 {
			if output != "" {
				output += "\n"
			}
			output += stderrBuf.String()
		}
		bgTask.AppendOutput([]byte(output))

		// Get exit code
		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			}
		}

		// Mark task as complete
		bgTask.Complete(exitCode, err)
	}()

	// Return immediately with task ID
	return toolresult.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("Task started in background.\nTask ID: %s\nPID: %d\nCommand: %s\nOutputFile: %s", bgTask.ID, bgTask.PID, command, bgTask.OutputFile),
		HookResponse: map[string]any{
			"backgroundTask": map[string]any{
				"taskId":      bgTask.ID,
				"description": description,
				"outputFile":  bgTask.OutputFile,
				"toolName":    t.Name(),
			},
		},
		Metadata: toolresult.ResultMetadata{
			Title:    t.Name(),
			Icon:     t.Icon(),
			Subtitle: fmt.Sprintf("[background] %s", bgTask.ID),
		},
	}
}

func prepareCwdTracking(command string) (string, string, func()) {
	tmp, err := os.CreateTemp("", "san-cwd-*")
	if err != nil {
		return command, "", func() {}
	}
	_ = tmp.Close()

	cleanup := func() {
		_ = os.Remove(tmp.Name())
	}
	wrapped := "trap 'pwd > \"$" + cwdFileEnvVar + "\"' EXIT\n" + command
	return wrapped, tmp.Name(), cleanup
}

func readTrackedCwd(path, fallback string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	newCwd := filepath.Clean(strings.TrimSpace(string(data)))
	if newCwd == "" || newCwd == "." || newCwd == filepath.Clean(fallback) {
		return ""
	}
	return newCwd
}

var extraEnvProvider atomic.Value // stores func(context.Context) []string

// SetEnvProvider registers a provider of additional environment variables
// for Bash child processes (e.g., plugin-injected variables). The
// provider is called with the per-invocation ctx so it can read
// per-call values (like the active plugin root) from the context.
func SetEnvProvider(fn func(context.Context) []string) {
	extraEnvProvider.Store(fn)
}

func bashEnv(ctx context.Context) []string {
	env := os.Environ()
	// Nudge common tools onto their non-interactive path so they fail fast
	// rather than block on a prompt. Set each only when the caller hasn't
	// already: os/exec keeps the last of duplicate keys, so an unconditional
	// append would silently override a value the user deliberately set.
	if _, ok := os.LookupEnv("GIT_TERMINAL_PROMPT"); !ok {
		env = append(env, "GIT_TERMINAL_PROMPT=0")
	}
	if _, ok := os.LookupEnv("DEBIAN_FRONTEND"); !ok {
		env = append(env, "DEBIAN_FRONTEND=noninteractive")
	}
	if fn, ok := extraEnvProvider.Load().(func(context.Context) []string); ok && fn != nil {
		env = append(env, fn(ctx)...)
	}
	return env
}

func init() {
	tool.Register(&BashTool{})
}
