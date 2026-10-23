package fs

import (
	"bytes"
	"io"
	"sync"
	"time"

	"github.com/genai-io/san/internal/tool"
)

// progressTailBytes is how much of the output's end a report carries — room
// for the longest preview (setting.MaxBashPreviewLines) of ordinary lines.
const progressTailBytes = 4 << 10

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
	tail       []byte
	lastReport time.Time
	pending    *time.Timer // reports what a throttled write left unreported
	stopped    bool
}

// tee counts the full stream even when its destination discards excess bytes.
func (p *outputProgress) tee(buf io.Writer) io.Writer {
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
	p.tail = append(p.tail, chunk[max(0, len(chunk)-progressTailBytes):]...)
	if over := len(p.tail) - progressTailBytes; over > 0 {
		p.tail = append(p.tail[:0], p.tail[over:]...)
	}
	lines, byteCount := p.lines, p.bytes
	fire := p.lastReport.IsZero() || time.Since(p.lastReport) >= progressInterval
	var tail string
	if fire { // copied only for a report: a fast command writes far more often
		p.lastReport = time.Now()
		tail = string(p.tail)
	} else if p.pending == nil {
		// Without this, output that stops inside the window would sit
		// unreported until the command ends.
		p.pending = time.AfterFunc(progressInterval-time.Since(p.lastReport), p.flush)
	}
	p.mu.Unlock()
	if fire {
		p.report(lines, byteCount, tail)
	}
}

func (p *outputProgress) flush() {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	p.pending = nil
	p.lastReport = time.Now()
	lines, byteCount, tail := p.lines, p.bytes, string(p.tail)
	p.mu.Unlock()
	p.report(lines, byteCount, tail)
}

// stop ends reporting once the command is done.
func (p *outputProgress) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopped = true
	if p.pending != nil {
		p.pending.Stop()
	}
}

// lastTail returns the end of the output seen so far.
func (p *outputProgress) lastTail() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return string(p.tail)
}

// countingWriter observes the accepted bytes without changing the destination's policy.
type countingWriter struct {
	buf      io.Writer
	progress *outputProgress
}

func (w *countingWriter) Write(chunk []byte) (int, error) {
	n, err := w.buf.Write(chunk)
	if n > 0 {
		w.progress.observe(n, chunk[:n])
	}
	return n, err
}
