package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/broker"
	"github.com/genai-io/san/internal/task"
)

func TestSendMessageRunningRouteFailure(t *testing.T) {
	for _, route := range []string{"absent", "rejecting", "stopped during send"} {
		t.Run(route, func(t *testing.T) {
			broker.Reset()
			t.Cleanup(broker.Reset)
			id := task.NewID()
			ctx := task.WithSessionID(context.Background(), "owner")
			worker := task.Default().CreateAgentTask(id, "worker", "test", ctx, func() {})
			t.Cleanup(func() { task.Default().Remove(id) })
			calls := 0
			if route != "absent" {
				broker.Register(id, func(broker.Message) bool {
					calls++
					if route == "stopped during send" {
						worker.Complete(context.Canceled)
					}
					return false
				})
			}
			result := NewSendMessageTool().Execute(ctx, map[string]any{"to": id, "message": "new instruction"}, ".")
			if result.Success {
				t.Fatalf("failed route reported success: %+v", result)
			}
			want := "still marked running"
			if route == "stopped during send" {
				want = "cannot be resumed automatically"
			}
			if !strings.Contains(result.Error, want) {
				t.Fatalf("error = %q, want %q", result.Error, want)
			}
			for _, misleading := range []string{"inbox is momentarily full", "still starting up", "send again", "wait a moment"} {
				if strings.Contains(result.Error, misleading) {
					t.Fatalf("misleading retry advice: %q", result.Error)
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
	for _, guidance := range []string{"new findings", "blockers needing a decision", "handoffs", "routine progress", "bare acknowledgments", "repeated unchanged blockers", "final results already delivered", "acknowledgment/reply loops", "requested answer"} {
		if !strings.Contains(description, guidance) {
			t.Errorf("schema missing cadence guidance %q", guidance)
		}
	}
}
