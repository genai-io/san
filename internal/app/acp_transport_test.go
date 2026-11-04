package app

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/genai-io/sdk-go/pkg/acp/protocol"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

type blockedACPWriter struct {
	io.WriteCloser
	block   atomic.Bool
	entered chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func (w *blockedACPWriter) Write(p []byte) (int, error) {
	if w.block.Load() {
		select {
		case w.entered <- struct{}{}:
		default:
		}
		<-w.closed
		return 0, io.ErrClosedPipe
	}
	return w.WriteCloser.Write(p)
}
func (w *blockedACPWriter) Close() error {
	w.once.Do(func() { close(w.closed) })
	return w.WriteCloser.Close()
}

// The pinned SDK requires connection shutdown to interrupt an active write.
// This is disconnect coverage, not a claim that session/cancel fixes slow peers.
func TestACPBlockedWriterDisconnect(t *testing.T) {
	var writer *blockedACPWriter
	f := newACPFixture(t, aitest.Always(aitest.Says("answer")), nil, func(out io.WriteCloser) io.WriteCloser {
		writer = &blockedACPWriter{WriteCloser: out, entered: make(chan struct{}, 1), closed: make(chan struct{})}
		return writer
	})
	id := f.session(t, t.TempDir())
	writer.block.Store(true)
	done := make(chan error, 1)
	go func() { _, err := f.prompt(id, "hello"); done <- err }()
	select {
	case <-writer.entered:
	case <-f.ctx.Done():
		t.Fatal("writer did not block")
	}
	if err := f.client.CancelPrompt(f.ctx, protocol.CancelNotification{SessionID: id}); err != nil {
		t.Fatal(err)
	}
	if err := f.client.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = f.endpoint.Wait(ctx)
	if ctx.Err() != nil {
		t.Fatal("blocked writer prevented callback join on disconnect")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("lost update reported success")
		}
	case <-ctx.Done():
		t.Fatal("prompt did not join")
	}
}
