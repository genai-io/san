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

// interactiveBash reports whether the interactive PTY path exists on this
// platform (unix). Bash uses its normal execution path when false.
const interactiveBash = true

// runInteractive runs cmd attached to a pseudo-terminal and answers interactive
// prompts through responder, returning the full combined output. A prompt the
// responder skips (ok=false), an exhausted answer budget, or a cancelled ctx
// closes the pty so the command fails fast rather than hanging.
func runInteractive(ctx context.Context, command string, cmd *exec.Cmd, responder BashPromptResponder) (string, error) {
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
			// Output went quiet. Only a tail that looks like an interactive prompt
			// is treated as waiting for input; a command working silently (a slow
			// build, a test run) is left to keep running — never answered or killed.
			if !processAlive(cmd) {
				rearm()
				continue
			}
			prompt := lastLine(pending.String())
			if !looksLikePrompt(prompt) {
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

// lastLine returns the last non-blank line of s (the prompt itself), trimmed. It
// scans backward so the growing pty buffer isn't copied and split on every stall.
func lastLine(s string) string {
	end := len(s)
	for end > 0 {
		if c := s[end-1]; c == '\n' || c == '\r' || c == ' ' || c == '\t' {
			end--
			continue
		}
		break
	}
	start := end
	for start > 0 && s[start-1] != '\n' && s[start-1] != '\r' {
		start--
	}
	return strings.TrimSpace(s[start:end])
}

// looksLikePrompt reports whether a stalled line is plausibly an interactive
// prompt awaiting input, rather than a command working silently. Gating on it
// keeps ordinary non-interactive output ("=== RUN TestX", build progress) from
// being mistaken for a prompt and killed.
func looksLikePrompt(line string) bool {
	if line == "" {
		return false
	}
	switch line[len(line)-1] {
	case '?', ':', ']', ')', '>':
		return true
	}
	return false
}
