package fs

import (
	"bytes"
	"io"
	"unicode/utf8"

	"github.com/genai-io/san/internal/task"
)

const maxShellOutputBytes = 30000

// Keep one extra byte so result formatting can detect truncation, while draining
// and counting all bytes without retaining them.
type shellOutput struct {
	buf   bytes.Buffer
	lines int
	last  byte
	total int64
}

func (b *shellOutput) Write(p []byte) (int, error) {
	if len(p) > 0 {
		b.lines += bytes.Count(p, []byte{'\n'})
		b.last = p[len(p)-1]
		b.total += int64(len(p))
		b.buf.Write(p[:min(len(p), maxShellOutputBytes+1-b.buf.Len())])
	}
	return len(p), nil
}

func (b *shellOutput) Bytes() []byte  { return b.buf.Bytes() }
func (b *shellOutput) String() string { return b.buf.String() }
func (b *shellOutput) Decoded() string {
	data := b.Bytes()
	if b.total > maxShellOutputBytes && !utf8.Valid(data) {
		for trim := 1; trim < utf8.UTFMax; trim++ {
			prefix := data[:len(data)-trim]
			if utf8.Valid(prefix) {
				return string(prefix) + "..."
			}
		}
	}
	return decodeOutput(data)
}
func (b *shellOutput) LineCount() int {
	if b.total > 0 && b.last != '\n' {
		return b.lines + 1
	}
	return b.lines
}

func truncateShellOutput(s string) string {
	if len(s) <= maxShellOutputBytes {
		return s
	}
	end := maxShellOutputBytes
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + "\n... (output truncated)"
}

// JSON task logs need complete UTF-8 characters, even across pipe reads.
type taskOutputWriter struct {
	task    *task.BashTask
	pending []byte
}

func (w *taskOutputWriter) Write(p []byte) (int, error) {
	data := append(w.pending, p...)
	end := len(data)
	start := end - 1
	for start > 0 && !utf8.RuneStart(data[start]) {
		start--
	}
	if start >= 0 && !utf8.FullRune(data[start:]) {
		end = start
	}
	w.task.AppendOutput(data[:end])
	w.pending = append([]byte(nil), data[end:]...)
	return len(p), nil
}

func copyTaskOutput(t *task.BashTask, r io.Reader) {
	w := &taskOutputWriter{task: t}
	// Hide WriterTo so a source cannot pass its entire output in one Write.
	_, _ = io.Copy(w, struct{ io.Reader }{outputReader(r)})
	if len(w.pending) > 0 {
		t.AppendOutput(w.pending)
	}
}
