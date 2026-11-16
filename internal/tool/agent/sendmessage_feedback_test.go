package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/broker"
	"github.com/genai-io/san/internal/task"
)

func TestSendMessageRunningRouteFailure(t *testing.T) {
	for _, route := range []string{"absent", "rejecting", "full", "wrapped full", "stopped during send"} {
		t.Run(route, func(t *testing.T) {
			broker.Reset()
			t.Cleanup(broker.Reset)
			id := task.NewID()
			ctx := task.WithSessionID(context.Background(), "owner")
			worker := task.Default().CreateAgentTask(id, "worker", "test", ctx, func() {})
			t.Cleanup(func() { task.Default().Remove(id) })
			calls := 0
			if route != "absent" {
				broker.RegisterAccept(id, func(broker.Message) error {
					calls++
					switch route {
					case "stopped during send":
						worker.Complete(context.Canceled)
					case "full":
						return broker.ErrInboxFull
					case "wrapped full":
						return fmt.Errorf("delivery rejected: %w", broker.ErrInboxFull)
					}
					return broker.ErrRejected
				})
			}
			result := NewSendMessageTool().Execute(ctx, map[string]any{"to": id, "message": "new instruction"}, ".")
			if result.Success {
				t.Fatalf("failed route reported success: %+v", result)
			}
			want := map[string]string{"full": "maximum number of unread messages", "wrapped full": "maximum number of unread messages", "stopped during send": "cannot be resumed automatically"}[route]
			if want == "" {
				want = "still marked running"
			}
			if !strings.Contains(result.Error, want) {
				t.Fatalf("error = %q, want %q", result.Error, want)
			}
			for _, misleading := range []string{"inbox is momentarily full", "still starting up", "send again", "wait a moment"} {
				if strings.Contains(result.Error, misleading) {
					t.Fatalf("misleading retry advice: %q", result.Error)
				}
			}
			if route == "full" || route == "wrapped full" {
				for _, guidance := range []string{"not queued", "will not be retried automatically", "do not retry or poll in a loop"} {
					if !strings.Contains(result.Error, guidance) {
						t.Errorf("queue-full feedback = %q, missing %q", result.Error, guidance)
					}
				}
				for _, misleading := range []string{"Do not resend", "only new actionable information"} {
					if strings.Contains(result.Error, misleading) {
						t.Errorf("queue-full feedback drops responsibility: %q", result.Error)
					}
				}
			}
			if calls > 1 {
				t.Fatalf("retried rejected delivery %d times", calls)
			}
		})
	}
}

func TestSendMessageSchemaCadence(t *testing.T) {
	description := NewSendMessageTool().Schema().Description
	for _, guidance := range []string{"actionable", "routine progress", "acknowledgment/reply loops"} {
		if !strings.Contains(description, guidance) {
			t.Errorf("schema missing cadence guidance %q", guidance)
		}
	}
}
