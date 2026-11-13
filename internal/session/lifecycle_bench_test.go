package session

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/genai-io/sdk-go/pkg/ai"
)

func BenchmarkSessionLifecycle(b *testing.B) {
	for _, count := range []int{100, 1000} {
		for _, operation := range []string{"Read", "Fork"} {
			b.Run(fmt.Sprintf("%s/messages=%d", operation, count), func(b *testing.B) {
				b.StopTimer()
				b.Setenv("HOME", b.TempDir())
				store, err := OpenStore(b.Context(), b.TempDir())
				if err != nil {
					b.Fatal(err)
				}
				defer store.Close()
				owner, err := store.Create(b.Context(), CreateOptions{ID: "benchmark-source"})
				if err != nil {
					b.Fatal(err)
				}
				defer owner.Close()
				messages := make([]ai.Message, count)
				for i := range messages {
					messages[i] = ai.Message{ID: fmt.Sprintf("message-%d", i), Role: ai.RoleUser, Content: ai.TextContent(strings.Repeat("x", 64))}
					if err := owner.Append(b.Context(), Event{Payload: MessageAppendPayload{Messages: []ai.Message{messages[i]}}}); err != nil {
						b.Fatal(err)
					}
				}
				b.ReportAllocs()
				b.ResetTimer()
				b.StartTimer()
				for i := 0; i < b.N; i++ {
					if operation == "Read" {
						projection, err := store.Read(b.Context(), owner.ID())
						if err != nil {
							b.Fatal(err)
						}
						b.StopTimer()
						if !reflect.DeepEqual(projection.Messages(), messages) {
							b.Fatal("read changed the committed history")
						}
					} else {
						child, err := store.Fork(b.Context(), owner.ID(), ForkOptions{})
						if err != nil {
							b.Fatal(err)
						}
						if err := child.Close(); err != nil {
							b.Fatal(err)
						}
						b.StopTimer()
						projection := child.Snapshot()
						if !reflect.DeepEqual(projection.Messages(), messages) {
							b.Fatal("fork changed the committed history")
						}
						if err := store.Delete(b.Context(), child.ID()); err != nil {
							b.Fatal(err)
						}
					}
					b.StartTimer()
				}
				b.StopTimer()
			})
		}
	}
}
