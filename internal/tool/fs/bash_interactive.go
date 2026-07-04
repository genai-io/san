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
	// eofByte is Ctrl-D: written to the pty it signals end-of-input to a command
	// blocked on read, without closing the master (which would not reliably wake
	// our reader on darwin) or killing a command that is not reading stdin.
	eofByte = 0x04
)

// runInteractive runs cmd attached to a pseudo-terminal and answers interactive
// prompts through responder, returning the full combined output. A skipped or
// unanswerable prompt is sent EOF (not a kill) so a real prompt aborts while a
// command working silently keeps running; only a cancelled ctx kills the process.
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
	stoppedAnswering := false

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
			trimToLine(&pending)
			rearm()

		case <-timer.C:
			// Output went quiet. Only a tail that heuristically looks like a prompt
			// is treated as waiting for input; a command working silently (a slow
			// build, a test run) is left to keep running.
			if stoppedAnswering || !processAlive(cmd) {
				rearm()
				continue
			}
			prompt := lastLine(pending.String())
			if !looksLikePrompt(prompt) {
				rearm()
				continue
			}

			var input string
			var ok bool
			if answers < maxAutoAnswers {
				answers++
				if isSecretPrompt(prompt) {
					input, ok = responder.RequestSecret(ctx, prompt)
				} else {
					input, ok = responder.AnswerPrompt(ctx, command, prompt)
				}
			}
			if ok {
				_, _ = ptmx.Write([]byte(input + "\n"))
			} else {
				// Declined, or the answer budget is spent: send EOF so a real
				// prompt reads empty and aborts, while a command not reading stdin
				// is unaffected — we never kill a working command. Stop here.
				_, _ = ptmx.Write([]byte{eofByte})
				stoppedAnswering = true
			}
			pending.Reset()
			rearm()
		}
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

// runWithResponder is the unix interactive path: it runs cmd on a pty and answers
// its prompts through responder, always handling the command. The platform-split
// partner (bash_interactive_other.go) returns handled=false off unix so bash
// falls back to its normal execution path.
func runWithResponder(ctx context.Context, command string, cmd *exec.Cmd, responder BashPromptResponder) (string, bool, error) {
	out, err := runInteractive(ctx, command, cmd, responder)
	return out, true, err
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

// trimToLine drops everything up to and including the last newline (\n or \r),
// keeping only the current unfinished line. A prompt has no trailing newline, so
// this bounds the pending buffer and keeps a completed line from being read as a
// prompt.
func trimToLine(pending *bytes.Buffer) {
	b := pending.Bytes()
	if i := bytes.LastIndexAny(b, "\r\n"); i >= 0 {
		tail := append([]byte(nil), b[i+1:]...)
		pending.Reset()
		pending.Write(tail)
	}
}

// lastLine returns the last non-blank line of s, trimmed. pending is bounded to a
// single line by trimToLine, so this operates on a small buffer.
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

// looksLikePrompt is a heuristic — it reports whether a stalled line's tail
// looks like an interactive prompt (ends with ? : ] ) >), not reliable prompt
// detection. It only gates the answer loop; a false positive is harmless because
// a declined prompt is sent EOF rather than killing the command.
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
