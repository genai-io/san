package app

import (
	"strings"
	"testing"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
)

func flushTestModel(msg core.ChatMessage) *model {
	m := &model{env: env{Width: 80}, conv: conv.NewModel(80)}
	m.conv.Messages = []core.ChatMessage{msg}
	return m
}

// The live welcome banner is visible from launch and tracks the model the user
// picks after launch — the regression behind #252, where the banner froze "no
// model selected" because it was committed to scrollback before any selection.
func TestLiveWelcomeTracksModelSelection(t *testing.T) {
	m := &model{env: env{Width: 80}, conv: conv.NewModel(80), welcomePending: true}

	// At launch, before a model is picked, the splash is already on screen.
	if got := m.liveWelcome(); !strings.Contains(got, "no model selected") {
		t.Fatalf("liveWelcome before selection = %q, want it to mention %q", got, "no model selected")
	}

	// Picking a model updates the live banner — it is not frozen.
	m.env.CurrentModel = &llm.CurrentModelInfo{ModelID: "claude-opus-4-8"}
	got := m.liveWelcome()
	if strings.Contains(got, "no model selected") {
		t.Fatalf("liveWelcome after selection still shows %q: %q", "no model selected", got)
	}
	if !strings.Contains(got, "claude-opus-4-8") {
		t.Fatalf("liveWelcome after selection = %q, want it to mention the picked model", got)
	}
}

// On the first commit the banner is frozen into scrollback with the selected
// model, and the live view stops drawing it (no duplicate).
func TestTakeWelcomeBannerFreezesAndClears(t *testing.T) {
	m := &model{env: env{Width: 80}, conv: conv.NewModel(80), welcomePending: true}
	m.env.CurrentModel = &llm.CurrentModelInfo{ModelID: "claude-opus-4-8"}

	banner := m.takeWelcomeBanner()
	if !strings.Contains(banner, "claude-opus-4-8") {
		t.Fatalf("frozen banner = %q, want it to mention the selected model", banner)
	}
	if m.welcomePending {
		t.Fatal("welcomePending should be cleared once the banner is frozen")
	}
	if got := m.liveWelcome(); got != "" {
		t.Fatalf("liveWelcome after freeze = %q, want \"\" (no duplicate in live view)", got)
	}
	if again := m.takeWelcomeBanner(); again != "" {
		t.Fatalf("takeWelcomeBanner is once-only, second call = %q", again)
	}
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
