package app

import (
	"strings"
	"testing"

	"github.com/genai-io/sdk-go/pkg/ai"

	"github.com/genai-io/san/internal/core"
)

func TestWithSideQuestion(t *testing.T) {
	call := ai.ToolCallBlock(ai.ToolCall{ID: "c1", Name: "Bash"})
	running := []core.Message{core.UserMessage("run tests", nil), {Role: ai.RoleAssistant, Content: ai.Content{call}}}

	got := withSideQuestion(running, "why?")
	if len(got) != 4 || len(running) != 2 {
		t.Fatalf("want pending results and question appended to a copy, got %d messages (chain now %d)", len(got), len(running))
	}
	if r := got[2].Content[0].ToolResult; r == nil || r.ToolCallID != "c1" || len(got[2].Content) != 1 {
		t.Fatalf("in-flight call must get a pending result of its own: %+v", got[2].Content)
	}
	// Text beside tool results is dropped by the OpenAI chat driver.
	if got[3].Content.Has(ai.BlockToolResult) || !strings.HasSuffix(got[3].Content.Text(), "why?") {
		t.Fatalf("question must be a message of its own: %+v", got[3].Content)
	}

	idle := []core.Message{core.UserMessage("hi", nil), core.AssistantMessage("hello", "", nil)}
	if got := withSideQuestion(idle, "why?"); len(got) != 3 {
		t.Fatalf("idle chain gets only the question, got %d messages", len(got))
	}

	// Left open, the request in flight reads as the side answer's to carry out.
	replying := []core.Message{core.UserMessage("write an essay", nil)}
	got = withSideQuestion(replying, "why?")
	if len(got) != 3 || got[1].Role != ai.RoleAssistant || got[1].Content.Text() != btwReplying {
		t.Fatalf("reply in flight must get a placeholder before the question: %+v", got)
	}
}

func TestBtwAnswerAfterSessionChangeIsDropped(t *testing.T) {
	m := &model{services: services{sessionID: "new"}}
	ask := btwAsk{question: "why?", sessionID: "old"}
	m.btw = []btwAsk{ask}

	m.onBtwAnswer(btwAnswerMsg{ask: ask, answer: "because"})
	if len(m.btw) != 0 {
		t.Fatalf("answered ask must leave the live rows: %+v", m.btw)
	}
	if len(m.conv.Messages) != 0 {
		t.Fatalf("answer to another session's question must not land here: %+v", m.conv.Messages)
	}
}
