package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/sdk-go/pkg/acp"
	"github.com/genai-io/sdk-go/pkg/acp/protocol"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

type blockedACPWriter struct {
	io.WriteCloser
	block   atomic.Bool
	matches func([]byte) bool
	entered chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func (w *blockedACPWriter) Write(p []byte) (int, error) {
	if w.block.Load() && (w.matches == nil || w.matches(p)) {
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

// Exercise the same endpoint setup and host teardown as RunACP with a short budget.
func TestACPWriteTimeout(t *testing.T) {
	for _, method := range []string{"session/update", "session/request_permission", ""} {
		t.Run(map[string]string{"session/update": "update", "session/request_permission": "permission", "": "final_response"}[method], func(t *testing.T) {
			cwd := t.TempDir()
			path := filepath.Join(cwd, "must-not-execute")
			input, _ := json.Marshal(map[string]string{"file_path": path, "content": "no"})
			driver := aitest.New(aitest.Says("other session"), aitest.Says("answer"))
			if method == "session/request_permission" {
				driver = aitest.New(aitest.Says("other session"), aitest.Asks(ai.ToolCall{ID: "blocked", Name: "Write", Input: string(input)}))
			}
			var writer *blockedACPWriter
			f := newACPFixtureWithTimeout(t, driver, nil, 100*time.Millisecond, func(out io.WriteCloser) io.WriteCloser {
				writer = &blockedACPWriter{WriteCloser: out, entered: make(chan struct{}, 1), closed: make(chan struct{}), matches: func(p []byte) bool {
					var frame struct{ Method string }
					return json.Unmarshal(p, &frame) == nil && frame.Method == method
				}}
				return writer
			})
			ids := []protocol.SessionID{f.session(t, cwd), f.session(t, t.TempDir())}
			if _, err := f.prompt(ids[1], "warm another runtime"); err != nil {
				t.Fatal(err)
			}
			var ended atomic.Int32
			for _, id := range ids {
				f.host.sessions[id].hooks.AddSessionFunctionHook(hook.SessionEnd, "", hook.FunctionHook{Callback: func(context.Context, hook.HookInput) (hook.HookOutput, error) {
					ended.Add(1)
					return hook.HookOutput{}, nil
				}})
			}
			receipts := make(chan *core.TurnFinalization, 1)
			s := f.host.sessions[ids[0]]
			record := s.runtimeBuildParams.OnEvent
			s.runtimeBuildParams.OnEvent = func(event core.Event) {
				record(event)
				if end, ok := event.(core.TurnEnded); ok {
					receipts <- end.Result.Finalization
				}
			}
			writer.block.Store(true)
			done := make(chan error, 1)
			go func() { _, err := f.prompt(ids[0], "hello"); done <- err }()
			select {
			case <-writer.entered:
			case <-f.ctx.Done():
				t.Fatal("target frame never blocked")
			}
			// No disconnect or prompt cancellation: the production SDK option must
			// interrupt the write and close every session on this connection.
			select {
			case err := <-f.stopped:
				if !errors.Is(err, acp.ErrWriteTimeout) {
					t.Fatalf("host exit = %v", err)
				}
			case <-f.ctx.Done():
				t.Fatal("timeout did not join host")
			}
			if err := <-done; err == nil {
				t.Fatal("undelivered frame reported successful prompt")
			}
			if ended.Load() != int32(len(ids)) {
				t.Fatalf("closed sessions = %d", ended.Load())
			}
			for _, s := range f.host.sessions {
				if s.runtime.Active() || len(s.runtime.Messages()) != 0 {
					t.Fatal("timeout retained runtime/history")
				}
			}
			select {
			case receipt := <-receipts:
				if err := receipt.Wait(f.ctx); err != nil {
					t.Fatalf("native finalization = %v", err)
				}
			default:
				t.Fatal("missing native finalization receipt")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("blocked permission executed tool")
			}
		})
	}
}

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
