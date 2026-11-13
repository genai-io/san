package app

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/acp/protocol"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

// acpBenchWriter paces only untimed transcript replay so it cannot overrun the
// fixture client's bounded notification queue. Timed prompts are unthrottled.
type acpBenchWriter struct {
	io.WriteCloser
	replay atomic.Bool
}

func (w *acpBenchWriter) Write(p []byte) (int, error) {
	if w.replay.Load() {
		time.Sleep(time.Millisecond)
	}
	return w.WriteCloser.Write(p)
}

// Both modes use native durable history and in-process ACP pipes. History counts
// messages (one real user/assistant pair per seed prompt), not fabricated users.
// Each iteration loads an independent fork of the same committed seed outside
// the timer. The timed work is a complete prompt, plus Stop and the prompt's
// automatic Resume in restart mode; neither recording nor inference is bypassed.
func BenchmarkACPPrompt(b *testing.B) {
	for _, size := range []int{0, 100, 1000} {
		for _, restart := range []bool{true, false} {
			b.Run(fmt.Sprintf("history=%d/restart=%v", size, restart), func(b *testing.B) {
				b.StopTimer()
				var writer *acpBenchWriter
				f := newACPFixture(b, aitest.Always(aitest.Says("done")), nil, func(w io.WriteCloser) io.WriteCloser {
					writer = &acpBenchWriter{WriteCloser: w}
					return writer
				})
				cwd := b.TempDir()
				warmup := f.session(b, cwd)
				if _, err := f.prompt(warmup, "warmup"); err != nil {
					b.Fatal(err)
				}
				seedID := f.session(b, cwd)
				seed := f.host.sessions[seedID]
				for i := 0; i < size/2; i++ {
					if _, err := f.prompt(seedID, "fixed history"); err != nil {
						b.Fatal(err)
					}
				}
				checkMessages := func(s *acpSession, want int) {
					b.Helper()
					projection := s.nativeSession.Session.Snapshot()
					if got := len(projection.ActiveMessages()); got != want {
						b.Fatalf("durable history: got %d messages, want %d", got, want)
					}
					if err := s.nativeSession.Session.Err(); err != nil {
						b.Fatal(err)
					}
				}
				checkMessages(seed, size)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					fork, err := seed.nativeSession.Store.Fork(f.ctx, string(seedID), session.ForkOptions{})
					if err != nil {
						b.Fatal(err)
					}
					id := protocol.SessionID(fork.ID())
					if err := fork.Close(); err != nil {
						b.Fatal(err)
					}
					writer.replay.Store(true)
					var loaded protocol.LoadSessionResponse
					if err := f.client.Call(f.ctx, "session/load", protocol.LoadSessionRequest{SessionID: id, CWD: cwd, MCPServers: []protocol.MCPServer{}}, &loaded); err != nil {
						b.Fatal(err)
					}
					writer.replay.Store(false)
					s := f.host.sessions[id]
					checkMessages(s, size)
					if got := len(s.runtime.Messages()); got != size {
						b.Fatalf("resumed history: got %d messages, want %d", got, size)
					}
					b.StartTimer()
					if restart {
						if err := s.runtime.Stop(context.Background()); err != nil {
							b.Fatal(err)
						}
					}
					if _, err := f.prompt(id, "hello"); err != nil {
						b.Fatal(err)
					}
					b.StopTimer()
					checkMessages(s, size+2)
					if err := s.close(); err != nil {
						b.Fatal(err)
					}
					f.host.mu.Lock()
					delete(f.host.sessions, id)
					f.host.mu.Unlock()
					// Retain only the fixed seed and warmup between iterations.
					if err := seed.nativeSession.Store.Delete(f.ctx, string(id)); err != nil {
						b.Fatal(err)
					}
					f.mu.Lock()
					f.updates = nil
					f.mu.Unlock()
				}
			})
		}
	}
}
