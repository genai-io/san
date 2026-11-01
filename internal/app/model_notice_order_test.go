package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/core"
)

func TestCompletionNoticeAboveLiveAgentAcrossCommit(t *testing.T) {
	m := fixedComposerModel(100, 40)
	call := core.ToolCall{ID: "worker", Name: "Agent", Input: `{"description":"live-worker","prompt":"work"}`}
	m.conv.Append(core.ChatMessage{Role: core.ChatAssistant, Content: "prior-answer"})
	m.showNoticeLine(mainNotice{FromAgent: true, Content: `<task-notification status="completed" description="notice-token">done</task-notification>`})
	m.conv.Append(core.ChatMessage{Role: core.ChatAssistant, ToolCalls: []core.ToolCall{call}})
	m.conv.Tool.PendingCalls = []core.ToolCall{call}
	check := func(phase string) {
		t.Helper()
		out := ansi.Strip(m.renderChatSection(conv.RenderActiveBody(m.messageRenderParams())))
		last := -1
		for _, token := range []string{"prior-answer", "notice-token", "live-worker"} {
			at := strings.Index(out, token)
			if at <= last || strings.Count(out, token) != 1 {
				t.Fatalf("%s: order/count of %s: %s", phase, token, out)
			}
			last = at
		}
	}
	check("first frame")
	short := ansi.Strip(m.chatTailAbove(strings.Repeat("\n", 38), m.messageRenderParams()))
	if notice, worker := strings.Index(short, "notice-token"), strings.Index(short, "live-worker"); notice < 0 || worker <= notice || strings.Count(short, "notice-token") != 1 {
		t.Fatalf("first compact frame: %s", short)
	}
	m.CommitMessages()
	if m.conv.CommittedCount != 2 {
		t.Fatalf("committed %d messages", m.conv.CommittedCount)
	}
	check("after commit")
	m.CommitMessages()
	check("repeat commit")
}
