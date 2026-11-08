package transcript

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/sdk-go/pkg/ai"
)

// These exercise the current San event codec/projector, not a synthetic row
// schema. Cache-cold means application/summary caches only: the OS page cache
// and SQLite pager are not flushed. No benchmark below claims cold disk I/O.
var backendBenchStores = []struct {
	name string
	open func(string, string) (*Store, error)
}{
	{"jsonl", NewStore},
	{"sqlite", NewSQLiteStore},
}

var backendBenchTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func backendBenchOpen(b *testing.B, open func(string, string) (*Store, error)) *Store {
	b.Helper()
	s, err := open(b.TempDir(), "bench")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := s.Close(); err != nil {
			b.Error(err)
		}
	})
	return s
}

// Four-message native tool exchange, including signed reasoning and a modest
// tool result. IDs and payloads are identical for both backends.
func backendBenchMessages(n int) []ai.Message {
	messages := make([]ai.Message, n)
	for i := range messages {
		callID := fmt.Sprintf("call-%04d", i/4)
		m := ai.Message{ID: fmt.Sprintf("m-%04d", i), Role: ai.RoleUser}
		switch i % 4 {
		case 0:
			m.Content = ai.TextContent("Inspect the session persistence implementation and explain its durability guarantees.")
		case 1:
			m.Role = ai.RoleAssistant
			m.Content = ai.Content{ai.ThinkingBlock("Read the implementation before drawing conclusions.", "signature"), ai.ToolCallBlock(ai.ToolCall{ID: callID, Name: "Read", Input: `{"path":"session.go"}`})}
		case 2:
			m.Content = ai.Content{ai.ToolResultBlock(ai.ToolResult{ToolCallID: callID, Content: ai.TextContent(strings.Repeat("persist the event before advancing the projection\n", 32))})}
		case 3:
			m.Role = ai.RoleAssistant
			m.Content = ai.TextContent("The writer persists events before advancing the in-memory projection; explicit sync establishes the durability boundary.")
		}
		messages[i] = m
	}
	return messages
}

func backendBenchSeed(b *testing.B, s *Store, id string, messages []ai.Message) {
	b.Helper()
	ctx := context.Background()
	release, err := s.Claim(id)
	if err != nil {
		b.Fatal(err)
	}
	defer func() {
		if err := release(); err != nil {
			b.Error(err)
		}
	}()
	if err := s.Start(ctx, StartCommand{SessionID: id, Time: backendBenchTime, Provider: "bench", Model: "native"}); err != nil {
		b.Fatal(err)
	}
	// An empty conversation saved in one batch emits the same native message
	// records without paying one fsync per fixture message before timing starts.
	if err := s.Append(ctx, Record{SessionID: id, Time: backendBenchTime, Type: ConversationReplaced, Messages: messages}); err != nil {
		b.Fatal(err)
	}
	if err := s.Append(ctx, Record{SessionID: id, Time: backendBenchTime, Type: NoticeAdded, Notice: &NoticeRecord{Text: "Fixture ready"}}); err != nil {
		b.Fatal(err)
	}
}

func backendBenchSize(b *testing.B, s *Store, id string) {
	b.Helper()
	info, err := s.log.Stat(context.Background(), id)
	if err != nil {
		b.Fatal(err)
	}
	// SDK logical transcript bytes, not database allocation or WAL footprint.
	b.ReportMetric(float64(info.Size), "logical-B/session")
}

func BenchmarkBackendDurableApplicationAppend(b *testing.B) {
	for _, backend := range backendBenchStores {
		b.Run(backend.name, func(b *testing.B) {
			s := backendBenchOpen(b, backend.open)
			backendBenchSeed(b, s, "session", backendBenchMessages(100))
			release, err := s.Claim("session")
			if err != nil {
				b.Fatal(err)
			}
			defer func() {
				if err := release(); err != nil {
					b.Error(err)
				}
			}()
			ctx := context.Background()
			if _, _, _, err := s.Position(ctx, "session"); err != nil {
				b.Fatal(err)
			}
			w := s.writers["session"]
			rec := Record{SessionID: "session", Time: backendBenchTime, Type: NoticeAdded, Notice: &NoticeRecord{AfterMessageID: "m-0099", Text: "Background task completed; results are available."}}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := s.Append(ctx, rec); err != nil {
					b.Fatal(err)
				}
				// A held JSONL writer otherwise buffers durability until lease release.
				// SQLite commits each append with synchronous=FULL already.
				if !s.sqlite {
					if err := w.Sync(); err != nil {
						b.Fatal(err)
					}
				}
			}
			b.StopTimer()
			backendBenchSize(b, s, "session")
		})
	}
}

// Save receives the full native conversation, just as San's writeRecord does.
// Reset to the same 100-message prefix outside timing, so each measured Save
// appends one message rather than encoding a conversation that grows with b.N.
// The physical history still accumulates resets. This is a durable Save cost,
// not a reset/compaction benchmark; reset costs are deliberately excluded.
func BenchmarkBackendNativeSaveAppend100(b *testing.B) {
	for _, backend := range backendBenchStores {
		b.Run(backend.name, func(b *testing.B) {
			s := backendBenchOpen(b, backend.open)
			messages := backendBenchMessages(101)
			backendBenchSeed(b, s, "session", messages[:100])
			w, err := s.log.Writer(context.Background(), "session")
			if err != nil {
				b.Fatal(err)
			}
			defer func() {
				if err := w.Close(); err != nil {
					b.Error(err)
				}
			}()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				if err := w.Save(messages[:100]); err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				// Changed Save already syncs JSONL and commits SQLite durably.
				if err := w.Save(messages); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
		})
	}
}

func BenchmarkBackendReplayResume(b *testing.B) {
	for _, backend := range backendBenchStores {
		for _, count := range []int{100, 1000} {
			b.Run(fmt.Sprintf("%s/messages=%d", backend.name, count), func(b *testing.B) {
				s := backendBenchOpen(b, backend.open)
				backendBenchSeed(b, s, "session", backendBenchMessages(count))
				ctx := context.Background()
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					b.StopTimer()
					clear(s.sessions)
					b.StartTimer()
					transcript, err := s.Load(ctx, "session")
					if err != nil {
						b.Fatal(err)
					}
					if len(transcript.Messages) != count {
						b.Fatalf("got %d messages, want %d", len(transcript.Messages), count)
					}
				}
				b.StopTimer()
				backendBenchSize(b, s, "session")
			})
		}
	}
}

func BenchmarkBackendList100(b *testing.B) {
	for _, backend := range backendBenchStores {
		for _, cache := range []string{"warm", "persisted-summary", "rebuild-summary"} {
			b.Run(backend.name+"/"+cache, func(b *testing.B) {
				s := backendBenchOpen(b, backend.open)
				messages := backendBenchMessages(100)
				ids := make([]string, 100)
				for i := range ids {
					ids[i] = fmt.Sprintf("session-%03d", i)
					backendBenchSeed(b, s, ids[i], messages)
				}
				ctx := context.Background()
				if items, err := s.List(ctx, "bench", ListOptions{}); err != nil || len(items) != len(ids) {
					b.Fatalf("seed list: %d items, %v", len(items), err)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if cache != "warm" {
						b.StopTimer()
						clear(s.summaries)
						// Preserve JSONL's existing disk cache in the realistic
						// reopen case; SQLite currently has only an in-memory cache.
						if cache == "rebuild-summary" && !s.sqlite {
							for _, id := range ids {
								if err := os.Remove(s.summaryPath(id)); err != nil && !os.IsNotExist(err) {
									b.Fatal(err)
								}
							}
						}
						b.StartTimer()
					}
					items, err := s.List(ctx, "bench", ListOptions{})
					if err != nil {
						b.Fatal(err)
					}
					if len(items) != len(ids) {
						b.Fatalf("got %d summaries, want %d", len(items), len(ids))
					}
				}
				b.StopTimer()
			})
		}
	}
}

func BenchmarkBackendFork100(b *testing.B) {
	for _, backend := range backendBenchStores {
		b.Run(backend.name, func(b *testing.B) {
			s := backendBenchOpen(b, backend.open)
			backendBenchSeed(b, s, "source", backendBenchMessages(100))
			ctx := context.Background()
			cmd := ForkCommand{SourceSessionID: "source", NewSessionID: "fork", Time: backendBenchTime}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := s.Fork(ctx, cmd); err != nil {
					b.Fatal(err)
				}
				b.StopTimer()
				if err := s.Delete(ctx, "fork"); err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
			}
			b.StopTimer()
		})
	}
}

// San serializes Load through Store.mu. This is the API-level globally locked
// baseline, not SDK independent-session writer scalability.
func BenchmarkBackendParallelLoad100(b *testing.B) {
	for _, backend := range backendBenchStores {
		b.Run(backend.name, func(b *testing.B) {
			s := backendBenchOpen(b, backend.open)
			backendBenchSeed(b, s, "session", backendBenchMessages(100))
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					transcript, err := s.Load(ctx, "session")
					if err != nil {
						b.Error(err)
						return
					}
					if len(transcript.Messages) != 100 {
						b.Errorf("got %d messages, want 100", len(transcript.Messages))
						return
					}
				}
			})
			b.StopTimer()
		})
	}
}
