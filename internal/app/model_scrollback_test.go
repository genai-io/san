package app

import (
	"testing"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/core"
)

func flushTestModel(msg core.ChatMessage) *model {
	m := &model{env: env{Width: 80}, conv: conv.NewModel(80)}
	m.conv.Messages = []core.ChatMessage{msg}
	return m
}

// A completed thinking paragraph (terminated by a blank line) commits to
// scrollback mid-stream, before any content arrives — reasoning no longer waits
// for the whole block to finish.
func TestFlushStreamingBlocksCommitsThinkingParagraph(t *testing.T) {
	m := flushTestModel(core.ChatMessage{
		Role:     core.RoleAssistant,
		Thinking: "first paragraph of reasoning\n\n",
	})

	if cmds := m.FlushStreamingBlocks(); len(cmds) == 0 {
		t.Fatal("a completed thinking paragraph should commit")
	}
	msg := m.conv.Messages[0]
	if msg.ThinkingCommittedLen != len(msg.Thinking) {
		t.Fatalf("ThinkingCommittedLen = %d, want %d", msg.ThinkingCommittedLen, len(msg.Thinking))
	}
	if !msg.ThinkingEmitted {
		t.Fatal("ThinkingEmitted should be set after the first thinking block commits")
	}
}

// The still-streaming trailing paragraph (no terminating blank line) stays in
// the live view until it completes — exactly like content's trailing block.
func TestFlushStreamingBlocksHoldsIncompleteThinking(t *testing.T) {
	m := flushTestModel(core.ChatMessage{
		Role:     core.RoleAssistant,
		Thinking: "still streaming this paragraph",
	})

	if cmds := m.FlushStreamingBlocks(); cmds != nil {
		t.Fatal("an incomplete thinking paragraph must stay in the live view")
	}
	if got := m.conv.Messages[0].ThinkingCommittedLen; got != 0 {
		t.Fatalf("ThinkingCommittedLen = %d, want 0 (nothing committed)", got)
	}
}

// When content starts — the reliable "reasoning done" signal — thinking's
// trailing paragraph is flushed too, so nothing reasoning-side lingers.
func TestFlushStreamingBlocksFlushesTrailingThinkingOnContent(t *testing.T) {
	m := flushTestModel(core.ChatMessage{
		Role:     core.RoleAssistant,
		Thinking: "reasoning with no trailing blank line",
		Content:  "Here",
	})

	if cmds := m.FlushStreamingBlocks(); len(cmds) == 0 {
		t.Fatal("content starting should flush the trailing thinking paragraph")
	}
	msg := m.conv.Messages[0]
	if msg.ThinkingCommittedLen != len(msg.Thinking) {
		t.Fatalf("thinking should be fully committed once content starts, got %d/%d",
			msg.ThinkingCommittedLen, len(msg.Thinking))
	}
}
