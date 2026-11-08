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

// These exercise San's native codec and projector. Cold caches refer only to
// application caches; the OS page cache is not flushed.
func jsonlBenchStore(b *testing.B, sessions, messages int) *Store {
	b.Helper()
	s, err := NewStore(b.TempDir(), "bench")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := s.Close(); err != nil {
			b.Error(err)
		}
	})
	conversation := make([]ai.Message, messages)
	for i := range conversation {
		m := ai.Message{ID: fmt.Sprintf("m-%04d", i), Role: ai.RoleUser}
		switch i % 4 {
		case 0:
			m.Content = ai.TextContent("Inspect session persistence and explain its durability guarantees.")
		case 1:
			m.Role = ai.RoleAssistant
			m.Content = ai.Content{ai.ThinkingBlock("Read the implementation.", "signature"), ai.ToolCallBlock(ai.ToolCall{ID: fmt.Sprintf("call-%04d", i/4), Name: "Read", Input: `{}`})}
		case 2:
			m.Content = ai.Content{ai.ToolResultBlock(ai.ToolResult{ToolCallID: fmt.Sprintf("call-%04d", i/4), Content: ai.TextContent(strings.Repeat("persist before advancing projection\n", 32))})}
		case 3:
			m.Role = ai.RoleAssistant
			m.Content = ai.TextContent("Explicit sync establishes the durability boundary.")
		}
		conversation[i] = m
	}
	ctx := context.Background()
	for i := 0; i < sessions; i++ {
		id := fmt.Sprintf("session-%03d", i)
		if err := s.Start(ctx, StartCommand{SessionID: id, Time: time.Now()}); err != nil {
			b.Fatal(err)
		}
		if err := s.Append(ctx, Record{SessionID: id, Type: ConversationReplaced, Messages: conversation}); err != nil {
			b.Fatal(err)
		}
	}
	return s
}

func BenchmarkJSONLDurableApplicationAppend(b *testing.B) {
	s := jsonlBenchStore(b, 1, 100)
	release, err := s.Claim("session-000")
	if err != nil {
		b.Fatal(err)
	}
	defer func() {
		if err := release(); err != nil {
			b.Error(err)
		}
	}()
	ctx := context.Background()
	if _, _, _, err := s.Position(ctx, "session-000"); err != nil {
		b.Fatal(err)
	}
	w := s.writers["session-000"]
	rec := Record{SessionID: "session-000", Type: NoticeAdded, Notice: &NoticeRecord{Text: "Background task completed."}}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := s.Append(ctx, rec); err != nil {
			b.Fatal(err)
		}
		// Application appends on a held writer otherwise sync at lease release.
		if err := w.Sync(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkJSONLReplayResume(b *testing.B) {
	for _, count := range []int{100, 1000} {
		b.Run(fmt.Sprintf("messages=%d", count), func(b *testing.B) {
			s := jsonlBenchStore(b, 1, count)
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				tx, err := s.Load(ctx, "session-000")
				if err != nil {
					b.Fatal(err)
				}
				if len(tx.Messages) != count {
					b.Fatalf("got %d messages, want %d", len(tx.Messages), count)
				}
			}
		})
	}
}

func BenchmarkJSONLList100(b *testing.B) {
	for _, cache := range []string{"warm", "persisted-summary", "rebuild-summary"} {
		b.Run(cache, func(b *testing.B) {
			s := jsonlBenchStore(b, 100, 100)
			ctx := context.Background()
			if items, err := s.List(ctx, "bench", ListOptions{}); err != nil || len(items) != 100 {
				b.Fatalf("seed list: %d items, %v", len(items), err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if cache != "warm" {
					b.StopTimer()
					clear(s.summaries)
					if cache == "rebuild-summary" {
						for i := 0; i < 100; i++ {
							if err := os.Remove(s.summaryPath(fmt.Sprintf("session-%03d", i))); err != nil {
								b.Fatal(err)
							}
						}
					}
					b.StartTimer()
				}
				items, err := s.List(ctx, "bench", ListOptions{})
				if err != nil || len(items) != 100 {
					b.Fatalf("list: %d items, %v", len(items), err)
				}
			}
		})
	}
}

func BenchmarkJSONLParallelHistory100(b *testing.B) {
	s := jsonlBenchStore(b, 1, 100)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			tx, err := s.LoadHistory(ctx, "session-000")
			if err != nil {
				b.Error(err)
				return
			}
			if len(tx.Messages) != 100 {
				b.Errorf("got %d messages, want 100", len(tx.Messages))
				return
			}
		}
	})
}
