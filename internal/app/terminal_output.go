package app

import (
	"bytes"
	"io"
	"sync"

	"github.com/charmbracelet/x/ansi"
)

// terminalOutput holds a handoff's redraw until its history insert has landed.
type terminalOutput struct {
	writer   io.Writer
	mu       sync.Mutex
	buffer   bytes.Buffer
	batching bool
	err      error
}

func (o *terminalOutput) Fd() uintptr {
	if file, ok := o.writer.(interface{ Fd() uintptr }); ok {
		return file.Fd()
	}
	return ^uintptr(0)
}

func (o *terminalOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.batching {
		return o.buffer.Write(p)
	}
	n, err := o.writer.Write(p)
	if err != nil && o.err == nil {
		o.err = err
	}
	return n, err
}

func (o *terminalOutput) Begin() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.batching = true
}

func (o *terminalOutput) Flush() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.batching = false
	data := o.buffer.Bytes()
	// A renderer flush ends mode 2026 before insertAbove; close it after both.
	if bytes.Contains(data, []byte(ansi.SetModeSynchronizedOutput)) {
		data = bytes.ReplaceAll(data, []byte(ansi.SetModeSynchronizedOutput), nil)
		data = bytes.ReplaceAll(data, []byte(ansi.ResetModeSynchronizedOutput), nil)
		data = append([]byte(ansi.SetModeSynchronizedOutput), data...)
		data = append(data, ansi.ResetModeSynchronizedOutput...)
	}
	if len(data) > 0 {
		n, err := o.writer.Write(data)
		if err == nil && n != len(data) {
			err = io.ErrShortWrite
		}
		if err != nil && o.err == nil {
			o.err = err
		}
	}
	o.buffer.Reset()
	return o.err
}
