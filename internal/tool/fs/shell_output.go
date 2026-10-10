package fs

import (
	"bytes"
	"io"
	"unicode/utf8"

	"github.com/genai-io/san/internal/task"
)

const maxShellOutputBytes = 30000

// Keep one extra byte to detect truncation; discarded output still counts as lines.
type outputCapture struct {
	buf      bytes.Buffer
	newlines int
	lastByte byte
}

func (b *outputCapture) Write(p []byte) (int, error) {
	if len(p) > 0 {
		b.newlines += bytes.Count(p, []byte{'\n'})
		b.lastByte = p[len(p)-1]
		b.buf.Write(p[:min(len(p), maxShellOutputBytes+1-b.buf.Len())])
	}
	return len(p), nil
}

func (b *outputCapture) String() string {
	data := b.buf.Bytes()
	if len(data) > maxShellOutputBytes && !utf8.Valid(data) {
		for trim := 1; trim < utf8.UTFMax; trim++ {
			prefix := data[:len(data)-trim]
			if utf8.Valid(prefix) {
				return string(prefix) + "..."
			}
		}
	}
	return decodeOutput(data)
}
func (b *outputCapture) LineCount() int {
	if b.buf.Len() > 0 && b.lastByte != '\n' {
		return b.newlines + 1
	}
	return b.newlines
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
func streamTaskOutput(bgTask *task.BashTask, r io.Reader) {
	r = outputReader(r)
	buf := make([]byte, 32*1024)
	pending := 0
	for {
		n, err := r.Read(buf[pending:])
		data := buf[:pending+n]
		end := len(data)
		if err == nil {
			start := end - 1
			for start > 0 && !utf8.RuneStart(data[start]) {
				start--
			}
			if start >= 0 && !utf8.FullRune(data[start:]) {
				end = start
			}
		}
		if end > 0 {
			bgTask.AppendOutput(data[:end])
		}
		pending = copy(buf, data[end:])
		if err != nil {
			return
		}
	}
}
