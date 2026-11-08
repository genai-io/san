package session

import (
	"fmt"
	"strings"
	"testing"

	"github.com/genai-io/sdk-go/pkg/ai"
)

// Each record is a separate commit, the worst case for historical copying.
func projectionBenchmarkBatches(records, contentBytes int) [][]Event {
	batches := make([][]Event, records)
	for i := range batches {
		var payload EventPayload = MessageAppendPayload{Messages: []ai.Message{eventText(fmt.Sprintf("message-%d", i), ai.RoleUser, strings.Repeat("x", contentBytes))}}
		if i == 0 {
			payload = SessionCreatePayload{ProjectID: "project", Workspace: "/work", InitialCWD: "/work", AgentID: "agent"}
		}
		batches[i] = []Event{{Seq: uint64(i + 1), Type: payload.eventType(), SchemaVersion: 1, Payload: payload}}
	}
	return batches
}

func BenchmarkEventProjection(b *testing.B) {
	for _, size := range []int{64, 16384} {
		for _, records := range []int{100, 1000} {
			batches := projectionBenchmarkBatches(records, size)
			name := fmt.Sprintf("records=%d/bytes=%d", records, size)
			b.Run("Replay/"+name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := ReplayEventBatches(testSession, batches, EventValidationContext{}); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("Append/"+name, func(b *testing.B) {
				base, err := ReplayEventBatches(testSession, batches[:len(batches)-1], EventValidationContext{})
				if err != nil {
					b.Fatal(err)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					if _, err := ApplyEventBatch(base, testSession, batches[len(batches)-1], EventValidationContext{}); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
