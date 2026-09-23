// The reasoning-display preference: "full" draws the body, "collapsed" replaces
// it with one duration line, "hidden" draws nothing. The two sites that can put
// reasoning on screen are the live view (conv.RenderAssistantMessage) and the
// scrollback flush (FlushStreamingBlocks → renderSnapshotCmd), and both must be
// gated — reasoning that reaches native scrollback cannot be taken back, so a
// gate on the live view alone would still leave every token permanent.
package app

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/todo"
)

// reasoningSentinel is the text that must never reach the screen in the
// suppressed modes.
const reasoningSentinel = "SECRET_REASONING_BODY"

// commitTestModel builds a model able to run the whole commit path
// (renderAndCommit → queueScrollbackPrint). That path assembles the live
// view's render params, which read the task tracker and the subagent registry,
// so both have to be real.
func commitTestModel(msgs ...core.ChatMessage) *model {
	m := &model{
		env:       env{Width: 80, Height: 24},
		conv:      conv.NewModel(80),
		userInput: input.New("", 80, nil, input.SelectorDeps{}),
		services:  services{Tracker: todo.NewStore(), Subagent: subagent.NewRegistry()},
	}
	m.conv.Messages = msgs
	return m
}

// queuedScrollbackPayload concatenates what the flush pipeline handed to the
// print queue — exactly the bytes that would land in native scrollback.
func queuedScrollbackPayload(m *model) string {
	var sb strings.Builder
	for _, p := range m.flush.pendingPrints {
		sb.WriteString(p.current)
		sb.WriteString(p.remaining)
	}
	return sb.String()
}

// full mode is the historical behaviour: the body commits to scrollback, and no
// summary line appears.
func TestFullThinkingCommitsTheBody(t *testing.T) {
	m := flushTestModel(core.ChatMessage{
		Role:             core.ChatAssistant,
		Thinking:         reasoningSentinel + "\n\n",
		ThinkingDuration: 3 * time.Second,
	})
	m.env.ThinkingDisplay = setting.ThinkingDisplayFull

	applyFlush(t, m, m.FlushStreamingBlocks())

	payload := queuedScrollbackPayload(m)
	if !strings.Contains(payload, reasoningSentinel) {
		t.Fatalf("full mode must commit the reasoning body, payload = %q", payload)
	}
	if strings.Contains(payload, "Thought") {
		t.Fatalf("full mode must not print a duration line, payload = %q", payload)
	}
}

// Collapsed: the body never reaches scrollback, and the block commits as a
// single duration line instead.
func TestCollapsedThinkingCommitsSummaryNotTheBody(t *testing.T) {
	m := flushTestModel(core.ChatMessage{
		Role: core.ChatAssistant,
		// Content arriving is the "reasoning is over" signal the flush waits
		// for, so a realistic message carries both.
		Thinking:         reasoningSentinel,
		Content:          "the answer",
		ThinkingDuration: 3200 * time.Millisecond,
	})
	m.env.ThinkingDisplay = setting.ThinkingDisplayCollapsed

	applyFlush(t, m, m.FlushStreamingBlocks())

	payload := queuedScrollbackPayload(m)
	if strings.Contains(payload, reasoningSentinel) {
		t.Fatalf("collapsed mode leaked the reasoning body into scrollback: %q", payload)
	}
	if !strings.Contains(ansi.Strip(payload), "Thought for 3.2s") {
		t.Fatalf("collapsed mode should commit one duration line, payload = %q", payload)
	}

	msg := m.conv.Messages[0]
	if msg.ThinkingCommittedLen != len(msg.Thinking) {
		t.Fatalf("ThinkingCommittedLen = %d, want %d — the offsets must advance past "+
			"reasoning that was suppressed, or a later rebuild renders the body it never showed",
			msg.ThinkingCommittedLen, len(msg.Thinking))
	}
	if !msg.ThinkingEmitted {
		t.Fatal("ThinkingEmitted must latch so the summary is printed once")
	}
}

// Hidden: neither the body nor a duration line, but the offsets still advance.
func TestHiddenThinkingCommitsNothing(t *testing.T) {
	m := flushTestModel(core.ChatMessage{
		Role: core.ChatAssistant,
		// A completed content block is what makes the flush ready at all; with
		// nothing complete there is no print to inspect.
		Thinking:         reasoningSentinel,
		Content:          "the answer\n\n",
		ThinkingDuration: 3 * time.Second,
	})
	m.env.ThinkingDisplay = setting.ThinkingDisplayHidden

	applyFlush(t, m, m.FlushStreamingBlocks())

	raw := queuedScrollbackPayload(m)
	payload := ansi.Strip(raw)
	if strings.Contains(raw, reasoningSentinel) {
		t.Fatalf("hidden mode leaked the reasoning body into scrollback: %q", raw)
	}
	if strings.Contains(payload, "Thought") {
		t.Fatalf("hidden mode must not print a duration line: %q", payload)
	}
	if !strings.Contains(payload, "the answer") {
		t.Fatalf("hidden mode must still commit the content: %q", payload)
	}
	if got := m.conv.Messages[0].ThinkingCommittedLen; got != len(reasoningSentinel) {
		t.Fatalf("ThinkingCommittedLen = %d, want %d", got, len(reasoningSentinel))
	}
}

// While reasoning is still streaming nothing commits for it in any mode: the
// collapsed summary would otherwise have to be rewritten, which native
// scrollback cannot do.
func TestCollapsedThinkingWaitsForReasoningToFinish(t *testing.T) {
	m := flushTestModel(core.ChatMessage{
		Role:     core.ChatAssistant,
		Thinking: reasoningSentinel + "\n\n", // a completed block, but no content yet
	})
	m.env.ThinkingDisplay = setting.ThinkingDisplayCollapsed

	if cmds := m.FlushStreamingBlocks(); cmds != nil {
		t.Fatal("reasoning that is not provably over must not commit a summary")
	}
	if got := m.conv.Messages[0].ThinkingCommittedLen; got != 0 {
		t.Fatalf("ThinkingCommittedLen = %d, want 0", got)
	}
}

// A rebuild of a settled message must not resurrect reasoning the mode
// suppressed. renderAndCommit renders through RenderSingleMessage, which is the
// path a turn-end commit takes.
func TestCollapsedThinkingIsNotResurrectedByTheTurnEndCommit(t *testing.T) {
	m := commitTestModel(core.ChatMessage{
		Role:             core.ChatAssistant,
		Thinking:         reasoningSentinel,
		Content:          "the answer",
		ThinkingDuration: 2 * time.Second,
	})
	m.env.ThinkingDisplay = setting.ThinkingDisplayCollapsed

	if cmds := m.commitAllMessages(); len(cmds) == 0 {
		t.Fatal("expected a commit payload for the uncommitted message")
	}
	payload := queuedScrollbackPayload(m)
	if strings.Contains(payload, reasoningSentinel) {
		t.Fatalf("the turn-end commit rendered suppressed reasoning: %q", payload)
	}
	if !strings.Contains(ansi.Strip(payload), "Thought for 2.0s") {
		t.Fatalf("the turn-end commit should carry the duration line: %q", payload)
	}
}
