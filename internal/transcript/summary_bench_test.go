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

// Keep large tool/audit payloads in the fixture: the picker needs neither.
func BenchmarkListSummaries(b *testing.B) {
	ctx := context.Background()
	dir := b.TempDir()
	store, err := NewStore(dir, "bench")
	if err != nil {
		b.Fatal(err)
	}
	for i := range 12 {
		id := fmt.Sprintf("session-%d", i)
		if err := store.Start(ctx, StartCommand{SessionID: id, Time: time.Now(), Sidechain: i >= 8}); err != nil {
			b.Fatal(err)
		}
		for j := range 20 {
			message := ai.Message{ID: fmt.Sprintf("m-%d", j), Role: ai.RoleUser, Content: ai.TextContent("hello")}
			if j%2 != 0 {
				message.Content = ai.Content{ai.ToolResultBlock(ai.ToolResult{ToolCallID: "call", Content: ai.TextContent(strings.Repeat("tool output\n", 8192))})}
			}
			if err := store.Append(ctx, Record{SessionID: id, Type: MessageAppended, Message: &message}); err != nil {
				b.Fatal(err)
			}
		}
	}
	for _, cold := range []bool{true, false} {
		b.Run(fmt.Sprintf("cold=%t", cold), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if cold {
					b.StopTimer()
					clear(store.summaries)
					for i := range 12 {
						_ = os.Remove(store.summaryPath(fmt.Sprintf("session-%d", i)))
					}
					b.StartTimer()
				}
				items, err := store.List(ctx, "bench", ListOptions{})
				if err != nil || len(items) != 8 {
					b.Fatalf("List = %d summaries, %v", len(items), err)
				}
			}
		})
	}
}
