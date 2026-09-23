// The resume replay window: a resumed session prints only its last N messages
// into native scrollback, and the messages it skips are announced rather than
// silently dropped.
package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/sdk-go/pkg/ai"

	"github.com/genai-io/san/internal/core"
)

// turnOf builds one assistant turn: a user prompt, an assistant message that
// calls a tool, and the tool's result. The result is what a naive cut can
// orphan, which is the whole reason the window snaps backwards.
func turnOf(tag string, toolCallID string) []core.ChatMessage {
	return []core.ChatMessage{
		{Role: core.ChatUser, Content: "prompt " + tag},
		{Role: core.ChatAssistant, Content: "thinking about " + tag, ToolCalls: []core.ToolCall{
			{ID: toolCallID, Name: "Bash", Input: `{"command":"echo ` + tag + `"}`},
		}},
		{Role: core.ChatUser, ToolResult: &core.ToolResult{
			ToolName: "Bash", ToolCallID: toolCallID, Content: ai.TextContent("out " + tag),
		}},
	}
}

// longTranscript is three complete turns, ending on a tool result — so a window
// of one or two messages does not fall on a turn boundary. Each turn is
// [user prompt, assistant with a tool call, tool result], so turn n starts at
// 3n, its assistant sits at 3n+1 and its result at 3n+2.
func longTranscript() []core.ChatMessage {
	var msgs []core.ChatMessage
	for i, tag := range []string{"a", "b", "c"} {
		msgs = append(msgs, turnOf(tag, "tc-"+string(rune('1'+i)))...)
	}
	return msgs
}

func TestResumeWindowStartSnapsToTurnBoundary(t *testing.T) {
	msgs := longTranscript() // 9 messages: results at 2, 5, 8

	tests := []struct {
		name string
		tail int
		want int
	}{
		{name: "tail covers the whole transcript", tail: 99, want: 0},
		{name: "tail of zero replays nothing", tail: 0, want: len(msgs)},
		{name: "negative tail replays nothing", tail: -3, want: len(msgs)},
		{name: "lands on an assistant boundary", tail: 6, want: 3},
		{name: "lands on a user prompt boundary", tail: 5, want: 4},
		// 9-2 = 7 is turn c's assistant: its result at 8 is inside the window,
		// so the pair stays whole and no snap is needed.
		{name: "assistant with its result in the window needs no snap", tail: 2, want: 7},
		// 9-1 = 8 is turn c's tool result, whose owning assistant at 7 is
		// outside; the window backs up so the result is not orphaned.
		{name: "trailing tool result snaps back to its assistant", tail: 1, want: 7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resumeWindowStart(msgs, tt.tail); got != tt.want {
				t.Fatalf("resumeWindowStart(tail=%d) = %d, want %d", tt.tail, got, tt.want)
			}
		})
	}
}

// A window that opens mid-turn would double-render: the tool result renders
// standalone *and* inline under its assistant, because pairing only looks
// forward from the window start. Assert the two agree.
func TestResumeWindowNeverOpensOnAToolResult(t *testing.T) {
	msgs := longTranscript()
	for tail := 0; tail <= len(msgs)+1; tail++ {
		start := resumeWindowStart(msgs, tail)
		if start < len(msgs) && msgs[start].ToolResult != nil {
			t.Fatalf("tail=%d opens the window on a tool result at %d", tail, start)
		}
	}
}

func TestApplyResumeWindowSeedsCommittedCount(t *testing.T) {
	m := commitTestModel(longTranscript()...)
	m.applyResumeWindow(1) // 9-1 = 8 is a tool result; snaps back to 7

	if m.conv.CommittedCount != 7 {
		t.Fatalf("CommittedCount = %d, want 7 (the snapped window start)", m.conv.CommittedCount)
	}
	if m.conv.ReplayStart != 7 {
		t.Fatalf("ReplayStart = %d, want 7", m.conv.ReplayStart)
	}
	if notice := m.conv.Messages[7]; notice.Role != core.ChatNotice || !strings.Contains(notice.Content, "7 messages earlier") {
		t.Fatalf("the window must open with a notice of what it skipped, got %+v", notice)
	}
	if len(m.conv.Messages) != 10 {
		t.Fatalf("the window must not discard messages: have %d, want 9 plus the notice", len(m.conv.Messages))
	}
}

// A window that skips nothing must not announce anything.
func TestApplyResumeWindowStaysSilentWhenNothingIsSkipped(t *testing.T) {
	m := commitTestModel(longTranscript()...)
	m.applyResumeWindow(99)

	if m.conv.CommittedCount != 0 {
		t.Fatalf("CommittedCount = %d, want 0", m.conv.CommittedCount)
	}
	for _, msg := range m.conv.Messages {
		if msg.Role == core.ChatNotice {
			t.Fatalf("a full replay has nothing to announce, got %q", msg.Content)
		}
	}
}

// The replay commits only the window, and opens it with the count it skipped —
// once. ReplayStart has to stay put after the replay prints, because /history
// reads it for the rest of the session.
func TestResumeReplayPrintsOnlyTheWindowAndAnnouncesTheRest(t *testing.T) {
	m := commitTestModel(longTranscript()...)
	m.applyResumeWindow(1) // window = [assistant c, result c], 7 skipped

	if cmds := m.commitAllMessages(); len(cmds) == 0 {
		t.Fatal("expected a commit payload for the replay window")
	}
	payload := ansi.Strip(queuedScrollbackPayload(m))

	if !strings.Contains(payload, "7 messages earlier not shown") {
		t.Fatalf("the skipped messages must be announced: %q", payload)
	}
	// The window is turn c: its assistant content and its tool result, and
	// nothing from turns a/b.
	if !strings.Contains(payload, "thinking about c") || !strings.Contains(payload, "out c") {
		t.Fatalf("the replay window must be printed: %q", payload)
	}
	for _, old := range []string{"prompt a", "prompt b", "out a", "out b"} {
		if strings.Contains(payload, old) {
			t.Fatalf("message %q is outside the window but was printed: %q", old, payload)
		}
	}
	if m.conv.ReplayStart != 7 {
		t.Fatalf("ReplayStart = %d after the replay, want 7 — /history reads it later",
			m.conv.ReplayStart)
	}

	// A second commit (the next turn ending) must not repeat the notice.
	m.flush.pendingPrints = nil
	m.conv.Messages = append(m.conv.Messages, core.ChatMessage{Role: core.ChatAssistant, Content: "next"})
	if cmds := m.commitAllMessages(); len(cmds) == 0 {
		t.Fatal("expected a commit payload for the new message")
	}
	again := ansi.Strip(queuedScrollbackPayload(m))
	if strings.Contains(again, "not shown") {
		t.Fatalf("the notice must be printed once: %q", again)
	}
	if !strings.Contains(again, "next") {
		t.Fatalf("the new message must still commit: %q", again)
	}
}

// resumeTailMessages: 0 replays nothing, so the notice is the whole replay. It
// must still print exactly once, not again with the next message.
func TestResumeReplayOfNothingPrintsTheNoticeOnce(t *testing.T) {
	m := commitTestModel(longTranscript()...)
	m.applyResumeWindow(0)

	if cmds := m.commitAllMessages(); len(cmds) == 0 {
		t.Fatal("expected the notice to commit")
	}
	if payload := ansi.Strip(queuedScrollbackPayload(m)); !strings.Contains(payload, "9 messages earlier not shown") {
		t.Fatalf("the notice must print: %q", payload)
	}

	m.flush.pendingPrints = nil
	m.conv.Messages = append(m.conv.Messages, core.ChatMessage{Role: core.ChatAssistant, Content: "next"})
	m.commitAllMessages()
	if again := ansi.Strip(queuedScrollbackPayload(m)); strings.Contains(again, "not shown") {
		t.Fatalf("the notice must be printed once: %q", again)
	}
}

// /history reads the skipped prefix through ReplayStart, which indexes
// into Messages. Anything that shortens the transcript must not turn the viewer
// into a slice panic, and an empty prefix must report nothing to show rather
// than opening an empty frame.
func TestRenderSkippedMessages(t *testing.T) {
	m := commitTestModel(longTranscript()...)
	m.applyResumeWindow(1)

	title, lines := m.renderSkippedMessages()
	if len(lines) == 0 {
		t.Fatal("the skipped prefix should render")
	}
	if !strings.Contains(title, "7 messages") {
		t.Fatalf("title = %q, want the skipped count", title)
	}
	joined := ansi.Strip(strings.Join(lines, "\n"))
	for _, want := range []string{"prompt a", "out a", "prompt b", "out b"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the skipped prefix is missing %q", want)
		}
	}
	if strings.Contains(joined, "thinking about c") {
		t.Error("the window is on screen, not skipped, and must not be repeated in /history")
	}

	// A transcript that shrank under the bound must clamp, not panic.
	m.conv.Messages = m.conv.Messages[:2]
	if _, lines := m.renderSkippedMessages(); len(lines) == 0 {
		t.Fatal("a clamped prefix should still render")
	}
	m.conv.Messages = nil
	if _, lines := m.renderSkippedMessages(); lines != nil {
		t.Fatalf("nothing to show should report nothing, got %d lines", len(lines))
	}
}
