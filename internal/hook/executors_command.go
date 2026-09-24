package hook

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/genai-io/san/internal/proc"
	"github.com/genai-io/san/internal/setting"
)

func (e *Engine) executeCommand(ctx context.Context, hookCmd setting.HookCmd, input HookInput) HookOutcome {
	outcome := HookOutcome{ShouldContinue: true}
	if hookCmd.Command == "" {
		return outcome
	}

	timeout := defaultTimeout
	if hookCmd.Timeout > 0 {
		timeout = hookCmd.Timeout
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	inputJSON, err := json.Marshal(input)
	if err != nil {
		outcome.Error = fmt.Errorf("failed to marshal input: %w", err)
		return outcome
	}

	cwd := e.getCwd()
	cmd := buildShellCommand(ctx, hookCmd, cwd)
	cmd.Stdin = bytes.NewReader(inputJSON)
	cmd.Env = e.buildEnv(ctx, input)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	exitCode := getExitCode(runErr)
	if exitCode < 0 {
		outcome.Error = runErr
		return outcome
	}
	if exitCode == 2 {
		return handleBlockingExit(&stderr)
	}
	if exitCode != 0 {
		// Report it. Leaving Error nil recorded the run as "ran" and dropped
		// stderr on the floor, so a hook that never worked — a typo, a missing
		// interpreter, exit 127 — looked identical to one that succeeded. The
		// user got no log line, no transcript record and no notice.
		//
		// ShouldContinue stays true: a non-zero exit that is not 2 is a failed
		// hook, not a blocking one, so the turn carries on.
		outcome.Error = commandFailure(exitCode, &stderr)
		return outcome
	}
	return e.parseOutput(strings.TrimSpace(stdout.String()), outcome)
}

// commandFailure describes a hook that exited non-zero, carrying the first
// line of its stderr — which is where the reason lives ("jq: command not
// found") and was previously discarded.
func commandFailure(exitCode int, stderr *bytes.Buffer) error {
	reason := strings.TrimSpace(stderr.String())
	if i := strings.IndexByte(reason, '\n'); i >= 0 {
		reason = reason[:i]
	}
	if reason == "" {
		return fmt.Errorf("hook exited %d", exitCode)
	}
	return fmt.Errorf("hook exited %d: %s", exitCode, reason)
}

func (e *Engine) executeCommandBidirectional(ctx context.Context, hookCmd setting.HookCmd, input HookInput) HookOutcome {
	outcome := HookOutcome{ShouldContinue: true}
	if hookCmd.Command == "" {
		return outcome
	}

	timeout := defaultTimeout
	if hookCmd.Timeout > 0 {
		timeout = hookCmd.Timeout
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	detached := false
	defer func() {
		if !detached {
			cancel()
		}
	}()

	inputJSON, err := json.Marshal(input)
	if err != nil {
		outcome.Error = fmt.Errorf("failed to marshal input: %w", err)
		return outcome
	}

	cwd := e.getCwd()
	cmd := buildShellCommand(ctx, hookCmd, cwd)
	cmd.Env = e.buildEnv(ctx, input)

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		outcome.Error = fmt.Errorf("failed to create stdin pipe: %w", err)
		return outcome
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		outcome.Error = fmt.Errorf("failed to create stdout pipe: %w", err)
		return outcome
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		outcome.Error = fmt.Errorf("failed to start hook: %w", err)
		return outcome
	}
	if _, err := io.WriteString(stdinPipe, string(inputJSON)+"\n"); err != nil {
		outcome.Error = fmt.Errorf("failed to write to stdin: %w", err)
		_ = cmd.Wait()
		return outcome
	}

	scanner := bufio.NewScanner(stdoutPipe)
	var finalOutput string
	firstLine := true
	promptCallback := e.getPromptCallback()

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if firstLine {
			firstLine = false
			var async asyncFirstLine
			if json.Unmarshal([]byte(line), &async) == nil && async.Async {
				detached = true
				go func() {
					defer cancel()
					_ = cmd.Wait()
				}()
				return outcome
			}
		}

		var promptReq PromptRequest
		if err := json.Unmarshal([]byte(line), &promptReq); err == nil && promptReq.Prompt != "" && promptReq.Message != "" {
			if promptCallback == nil {
				continue
			}
			resp, cancelled := promptCallback(promptReq)
			if cancelled {
				_ = stdinPipe.Close()
				_ = cmd.Wait()
				return outcome
			}
			respJSON, err := json.Marshal(resp)
			if err != nil {
				continue
			}
			if _, err := io.WriteString(stdinPipe, string(respJSON)+"\n"); err != nil {
				break
			}
			continue
		}
		finalOutput = line
	}
	exitCode := getExitCode(cmd.Wait())
	if exitCode == 2 {
		return handleBlockingExit(&stderr)
	}
	if exitCode != 0 && exitCode >= 0 {
		return outcome
	}
	return e.parseOutput(finalOutput, outcome)
}

// buildShellCommand prepares a hook process. Hook commands talk through
// configured pipes, never the TUI's controlling terminal — an interactive hook
// means the JSON line protocol, not terminal ownership — so the child is
// detached, and cancellation reaches its whole process group.
func buildShellCommand(ctx context.Context, hookCmd setting.HookCmd, cwd string) *exec.Cmd {
	name, args := hookInvocation(hookCmd)
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = cwd
	proc.DetachSession(cmd)
	cmd.Cancel = func() error {
		_ = proc.TerminateGroup(cmd, syscall.SIGKILL)
		return nil
	}
	// Backstop: if a grandchild keeps the stdout/stderr pipe open after the
	// shell is killed (common on Windows where we can't group-kill), give Wait
	// a bounded time to drain before exec force-closes the pipes.
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

// hookInvocation is the program and arguments a hook command runs as.
//
// On Windows a command that is just the path of a script runs that script
// rather than going through a shell's parser, which would read the
// backslashes in "C:\hooks\check.sh" as escapes.
func hookInvocation(hookCmd setting.HookCmd) (string, []string) {
	shell := strings.ToLower(strings.TrimSpace(hookCmd.Shell))
	if runtime.GOOS == "windows" && shell == "" {
		script := strings.Trim(strings.TrimSpace(hookCmd.Command), `"`)
		if info, err := os.Stat(script); err == nil && !info.IsDir() {
			switch strings.ToLower(filepath.Ext(script)) {
			case ".exe", ".bat", ".cmd":
				return script, nil
			case ".ps1":
				return powerShell(), append(powerShellFlags(), "-File", script)
			default:
				if bash, ok := proc.BashPath(); ok {
					return bash, []string{script}
				}
			}
		}
	}
	name, args := hookShell(shell)
	return name, append(args, hookCmd.Command)
}

// hookShell picks the interpreter for a hook command. PowerShell means pwsh
// when installed, else the Windows PowerShell every Windows ships with.
// Otherwise it is sh on Unix; Windows has no sh of its own, so there it is Git
// for Windows' bash when installed, else PowerShell.
func hookShell(shell string) (string, []string) {
	powerShellArgs := append(powerShellFlags(), "-Command")
	switch {
	case shell == "powershell" || shell == "pwsh":
		return powerShell(), powerShellArgs
	case runtime.GOOS != "windows":
		return "sh", []string{"-c"}
	}
	if bash, ok := proc.BashPath(); ok {
		return bash, []string{"-c"}
	}
	return powerShell(), powerShellArgs
}

// powerShellFlags run PowerShell without the user's profile or a prompt.
func powerShellFlags() []string { return []string{"-NoProfile", "-NonInteractive"} }

// powerShell is pwsh or Windows PowerShell, whichever resolves; the bare name
// when neither does, so the failure names what is missing.
func powerShell() string {
	if path, ok := proc.PowerShellPath(); ok {
		return path
	}
	return "powershell"
}

func handleBlockingExit(stderr *bytes.Buffer) HookOutcome {
	reason := strings.TrimSpace(stderr.String())
	if reason == "" {
		reason = "Hook blocked execution"
	}
	return HookOutcome{
		ShouldContinue: false,
		ShouldBlock:    true,
		BlockReason:    reason,
	}
}
