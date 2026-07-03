//go:build unix

package fs

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/creack/pty"
)

const (
	// promptStallDelay is how long output must be quiet before a still-running
	// command is treated as waiting for input.
	promptStallDelay = 400 * time.Millisecond
	// maxAutoAnswers bounds how many prompts one command may raise, so a
	// misbehaving process can't loop us forever.
	maxAutoAnswers = 12
)

// runInteractive runs cmd attached to a pseudo-terminal and answers interactive
// prompts through responder, returning the full combined output. A prompt the
// responder skips (ok=false), an exhausted answer budget, or a cancelled ctx
// closes the pty so the command fails fast rather than hanging.
func runInteractive(ctx context.Context, command string, cmd *exec.Cmd, responder PromptResponder) (string, error) {
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return "", err
	}
	defer func() { _ = ptmx.Close() }()

	chunks := make(chan []byte, 16)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				chunks <- append([]byte(nil), buf[:n]...)
			}
			if err != nil {
				close(chunks)
				return
			}
		}
	}()

	var out, pending bytes.Buffer
	answers := 0
	skip := false

	timer := time.NewTimer(promptStallDelay)
	defer timer.Stop()
	rearm := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(promptStallDelay)
	}

loop:
	for {
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			break loop

		case b, ok := <-chunks:
			if !ok {
				break loop // process finished and closed the pty
			}
			out.Write(b)
			pending.Write(b)
			rearm()

		case <-timer.C:
			// Output went quiet. If the process is still alive it is most likely
			// blocked on input.
			if !processAlive(cmd) {
				rearm()
				continue
			}
			prompt := lastLine(pending.String())
			if prompt == "" {
				rearm()
				continue
			}
			if answers >= maxAutoAnswers {
				skip = true
				break loop
			}
			answers++

			var input string
			var ok bool
			if isSecretPrompt(prompt) {
				input, ok = responder.RequestSecret(ctx, prompt)
			} else {
				input, ok = responder.AnswerPrompt(ctx, command, prompt)
			}
			if !ok {
				skip = true
				break loop
			}
			_, _ = ptmx.Write([]byte(input + "\n"))
			pending.Reset()
			rearm()
		}
	}

	if skip {
		// Terminate so the reader hits EOF and unblocks. Closing the master
		// alone does not reliably wake a blocked Read on darwin; ending the
		// process makes the kernel deliver EOF the same way a normal exit does.
		// The command fails for lack of input — the fail-fast outcome we want.
		_ = cmd.Process.Kill()
	}
	for b := range chunks { // drain whatever the process emitted on its way out
		out.Write(b)
	}

	werr := cmd.Wait()
	if ctx.Err() != nil {
		return out.String(), ctx.Err()
	}
	return out.String(), werr
}

// processAlive reports whether the command's process is still running, without
// reaping it (signal 0 performs error checking only).
func processAlive(cmd *exec.Cmd) bool {
	if cmd.Process == nil {
		return false
	}
	return cmd.Process.Signal(syscall.Signal(0)) == nil
}

// secretPromptMarkers identify a prompt asking for a credential. Matching one
// routes the prompt to RequestSecret so the value never reaches a model.
var secretPromptMarkers = []string{"password", "passphrase", "[sudo]", "secret key", "pin:"}

// isSecretPrompt reports whether a prompt is asking for a credential.
func isSecretPrompt(prompt string) bool {
	lower := strings.ToLower(prompt)
	for _, m := range secretPromptMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

// lastLine returns the last non-blank line of s (the prompt itself), with
// carriage returns and surrounding space stripped.
func lastLine(s string) string {
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}
