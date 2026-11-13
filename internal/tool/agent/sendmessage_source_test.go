package agent

import (
	"context"
	"testing"

	"github.com/genai-io/san/internal/broker"
	"github.com/genai-io/san/internal/tool"
)

func TestSendMessageProvenanceComesOnlyFromTrustedContext(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		t.Run(map[bool]string{false: "host-origin", true: "native-call"}[trusted], func(t *testing.T) {
			route := "provenance-receiver"
			var got broker.Message
			broker.Register(route, func(m broker.Message) bool { got = m; return true })
			defer broker.Unregister(route)
			ctx := context.Background()
			if trusted {
				ctx = tool.WithMessageSource(ctx, "original-session", 42)
			}
			result := NewSendMessageTool().Execute(ctx, map[string]any{"to": route, "message": "interim report", "source_session": "forged", "source_seq": 999}, ".")
			if !result.Success {
				t.Fatal(result.Error)
			}
			wantSession, wantSeq := "", uint64(0)
			if trusted {
				wantSession, wantSeq = "original-session", 42
			}
			if got.SourceSession != wantSession || got.SourceSeq != wantSeq {
				t.Fatalf("untrusted provenance: %+v", got)
			}
		})
	}
}
