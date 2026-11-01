package conv

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/core"
)

func TestToolActivityFollowsNoticesWithoutMovingText(t *testing.T) {
	call := core.ToolCall{ID: "worker", Name: "Agent", Input: `{"description":"live-worker","prompt":"work"}`}
	for _, notice := range []core.ChatMessage{
		{Role: core.ChatNotice, Content: "notice-token"},
		{Role: core.ChatNotice, AgentNotice: true, Content: "notice-token"},
		{Role: core.ChatUser, Content: `<task-notification status="completed" description="notice-token">done</task-notification>`},
	} {
		messages := []core.ChatMessage{
			{Role: core.ChatUser, Content: "user-before"},
			{Role: core.ChatAssistant, Content: "assistant-before", ToolCalls: []core.ToolCall{call}},
			notice,
			{Role: core.ChatNotice, Content: "second-notice"},
			{Role: core.ChatUser, Content: "user-after"},
			{Role: core.ChatAssistant, Content: "assistant-after"},
		}
		before := append([]core.ChatMessage(nil), messages...)
		p := RenderContext{Messages: messages, Width: 100, PendingCalls: []core.ToolCall{call}}
		out := ansi.Strip(RenderActiveBody(p))
		last := -1
		for _, token := range []string{"user-before", "assistant-before", "notice-token", "second-notice", "live-worker", "user-after", "assistant-after"} {
			at := strings.Index(out, token)
			if at <= last || strings.Count(out, token) != 1 {
				t.Fatalf("order/count for %s: %s", token, out)
			}
			last = at
		}
		if !reflect.DeepEqual(messages, before) {
			t.Fatal("render changed transcript")
		}
		history := ansi.Strip(RenderMessageRange(p, 0, len(messages), false))
		if strings.Index(history, "live-worker") >= strings.Index(history, "notice-token") {
			t.Fatalf("history no longer follows transcript order: %s", history)
		}
		// Exercise each notice representation as the compact layout's newest row.
		p.Messages = messages[:3]
		for _, height := range []int{2, 4, 20} {
			compact, ok := RenderActiveWorkers(p, height)
			compact = ansi.Strip(compact)
			if !ok || strings.Index(compact, "notice-token") < 0 || strings.Index(compact, "live-worker") <= strings.Index(compact, "notice-token") {
				t.Fatalf("compact order: %s", compact)
			}
		}
	}
}
