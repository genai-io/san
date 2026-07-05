package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
)

func flushTestModel(msg core.ChatMessage) *model {
	m := &model{env: env{Width: 80}, conv: conv.NewModel(80)}
	m.conv.Messages = []core.ChatMessage{msg}
	return m
}

// applyFlush runs the off-thread render Cmd that FlushStreamingBlocks kicked off
// and lands its result, mirroring the real render → handleFlushResult path
// so tests can assert the committed offsets the landing advances.
func applyFlush(t *testing.T, m *model, cmds []tea.Cmd) {
	t.Helper()
	if len(cmds) == 0 {
		t.Fatal("expected a flush render Cmd, got none")
	}
	msg := cmds[0]()
	br, ok := msg.(flushResultMsg)
	if !ok {
		t.Fatalf("flush Cmd returned %T, want flushResultMsg", msg)
	}
	m.handleFlushResult(br)
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
// for the whole block to finish. The render runs off-thread; the committed
// offset advances only once it lands.
func TestFlushStreamingBlocksCommitsThinkingParagraph(t *testing.T) {
	m := flushTestModel(core.ChatMessage{
		Role:     core.RoleAssistant,
		Thinking: "first paragraph of reasoning\n\n",
	})

	applyFlush(t, m, m.FlushStreamingBlocks())

	msg := m.conv.Messages[0]
	if msg.ThinkingCommittedLen != len(msg.Thinking) {
		t.Fatalf("ThinkingCommittedLen = %d, want %d", msg.ThinkingCommittedLen, len(msg.Thinking))
	}
	if !msg.ThinkingEmitted {
		t.Fatal("ThinkingEmitted should be set after the first thinking block commits")
	}
	if m.flush.rendering {
		t.Fatal("flush.rendering should clear once the render has landed")
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

	applyFlush(t, m, m.FlushStreamingBlocks())

	msg := m.conv.Messages[0]
	if msg.ThinkingCommittedLen != len(msg.Thinking) {
		t.Fatalf("thinking should be fully committed once content starts, got %d/%d",
			msg.ThinkingCommittedLen, len(msg.Thinking))
	}
}

// Only one block render is in flight at a time: while one is rendering off-
// thread, a second flush is suppressed so the scrollback Printlns stay ordered.
func TestFlushStreamingBlocksGatesWhileRendering(t *testing.T) {
	m := flushTestModel(core.ChatMessage{
		Role:    core.RoleAssistant,
		Content: "first block\n\nsecond block\n\n",
	})

	if cmds := m.FlushStreamingBlocks(); len(cmds) == 0 {
		t.Fatal("the first completed block should start a render")
	}
	if !m.flush.rendering {
		t.Fatal("flush.rendering should latch while a render is in flight")
	}
	if cmds := m.FlushStreamingBlocks(); cmds != nil {
		t.Fatal("a second flush must be suppressed while one render is in flight")
	}
}

// Tables are rendered very differently after commit (bordered table) than while
// streaming (plain markdown source). Keep them in the live tail until turn end
// so insertAbove never has to replace raw table rows with a taller committed
// table mid-stream.
func TestFlushStreamingBlocksHoldsTablesUntilTurnEnd(t *testing.T) {
	m := flushTestModel(core.ChatMessage{
		Role:    core.RoleAssistant,
		Content: "| Kubernetes 组件 | 类比 |\n|---|---|\n| kube-apiserver | 总服务窗口 |\n\n",
	})

	if cmds := m.FlushStreamingBlocks(); cmds != nil {
		t.Fatal("a completed markdown table must wait for turn-end commit")
	}
	if got := m.conv.Messages[0].ContentCommittedLen; got != 0 {
		t.Fatalf("ContentCommittedLen = %d, want 0 (table not progressively committed)", got)
	}
}

// A render that lands after its row was already committed whole (turn-end or
// cancel commits the remainder, in-flight block included) is dropped — no
// duplicate Println, and flush.rendering still clears.
func TestHandleFlushResultDiscardsCommittedRow(t *testing.T) {
	m := flushTestModel(core.ChatMessage{
		Role:    core.RoleAssistant,
		Content: "a block\n\n",
	})

	cmds := m.FlushStreamingBlocks()
	if len(cmds) == 0 {
		t.Fatal("expected a render Cmd")
	}
	br, ok := cmds[0]().(flushResultMsg)
	if !ok {
		t.Fatal("flush Cmd did not return a flushResultMsg")
	}

	// The row got committed to scrollback before the render landed.
	m.conv.CommittedCount = 1
	if cmd := m.handleFlushResult(br); cmd != nil {
		t.Fatal("a render for an already-committed row must be discarded (no Println)")
	}
	if m.flush.rendering {
		t.Fatal("flush.rendering must clear even when the render is discarded")
	}
}

// A render that lands after its row was dropped and replaced by a retry's fresh
// row (new message ID) is dropped rather than corrupting the new row's offsets.
func TestHandleFlushResultDiscardsReplacedRow(t *testing.T) {
	m := flushTestModel(core.ChatMessage{
		Role:    core.RoleAssistant,
		ID:      "old",
		Content: "a block\n\n",
	})

	cmds := m.FlushStreamingBlocks()
	if len(cmds) == 0 {
		t.Fatal("expected a render Cmd")
	}
	br := cmds[0]().(flushResultMsg)

	// Retry dropped the streaming row and appended a fresh one (new ID).
	m.conv.Messages[0] = core.ChatMessage{Role: core.RoleAssistant, ID: "new", Content: "retried"}
	if cmd := m.handleFlushResult(br); cmd != nil {
		t.Fatal("a render for a replaced row must be discarded")
	}
	if got := m.conv.Messages[0].ContentCommittedLen; got != 0 {
		t.Fatalf("the fresh row's ContentCommittedLen = %d, want 0 (stale render must not advance it)", got)
	}
}
