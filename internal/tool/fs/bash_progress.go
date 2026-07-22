package fs

import (
	"bytes"
	"io"
	"sync"
	"time"

	"github.com/genai-io/san/internal/tool"
)

// progressInterval throttles how often a running command reports its growing
// output: often enough to feel live, rarely enough not to flood the UI channel.
const progressInterval = 120 * time.Millisecond

// outputProgress accumulates the line and byte totals of a running command's
// output and forwards throttled snapshots to a reporter. stdout and stderr are
// copied on separate goroutines, so every field is mutex-guarded and the count
// is shared across both streams — the reporter sees one combined total.
type outputProgress struct {
	report tool.BashProgress

	mu         sync.Mutex
	lines      int
	bytes      int64
	lastReport time.Time
}

// tee wraps buf so writes to it also feed the counter. The returned writer is
// the command's stdout or stderr; buf still receives the full output verbatim.
func (p *outputProgress) tee(buf *bytes.Buffer) io.Writer {
	if p == nil || p.report == nil {
		return buf
	}
	return &countingWriter{buf: buf, progress: p}
}

// observe folds one chunk into the running totals and reports the new count when
// the throttle window has elapsed. The reporter is invoked outside the lock so a
// slow consumer never stalls the command's output copy.
func (p *outputProgress) observe(n int, chunk []byte) {
	p.mu.Lock()
	p.bytes += int64(n)
	p.lines += bytes.Count(chunk, []byte{'\n'})
	lines, byteCount := p.lines, p.bytes
	fire := p.lastReport.IsZero() || time.Since(p.lastReport) >= progressInterval
	if fire {
		p.lastReport = time.Now()
	}
	p.mu.Unlock()
	if fire {
		p.report(lines, byteCount)
	}
}

// countingWriter forwards every write to the underlying buffer unchanged, then
// counts it. It never alters or withholds bytes, so the command's captured
// output is identical to writing straight to the buffer.
type countingWriter struct {
	buf      *bytes.Buffer
	progress *outputProgress
}

func (w *countingWriter) Write(chunk []byte) (int, error) {
	n, err := w.buf.Write(chunk)
	if n > 0 {
		w.progress.observe(n, chunk[:n])
	}
	return n, err
}
