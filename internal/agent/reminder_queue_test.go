package agent

import (
	"strings"
	"sync"
	"testing"

	"github.com/genai-io/san/internal/agent/reminder"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestReminderEnqueueAndDrain(t *testing.T) {
	s := NewReminderQueue()
	if !s.Empty() {
		t.Fatal("new reminder should be empty")
	}

	s.Enqueue("first")
	s.Enqueue("second")
	s.Enqueue("") // dropped

	out := s.Drain()
	if len(out) != 2 {
		t.Fatalf("expected 2 reminders, got %d: %v", len(out), out)
	}
	if !strings.Contains(out[0], "first") {
		t.Errorf("first reminder missing body: %q", out[0])
	}
	if !strings.Contains(out[1], "second") {
		t.Errorf("second reminder missing body: %q", out[1])
	}
	if !s.Empty() {
		t.Error("reminder should be empty after Drain")
	}
}

func TestReminderProviderRegistration(t *testing.T) {
	s := NewReminderQueue()

	rendered := 0
	s.Register("skills", func() string {
		rendered++
		return "- foo: do foo"
	})

	s.RequeueSystemReminders()
	if rendered != 1 {
		t.Fatalf("provider Body should be called once, got %d", rendered)
	}

	out := s.Drain()
	if len(out) != 1 || !strings.Contains(out[0], "- foo: do foo") {
		t.Errorf("provider output not enqueued: %v", out)
	}
}

func TestReminderProviderReplaceByID(t *testing.T) {
	s := NewReminderQueue()
	s.Register("skills", func() string { return "old" })
	s.Register("skills", func() string { return "new" })

	s.RequeueSystemReminders()
	out := s.Drain()
	if len(out) != 1 {
		t.Fatalf("registering same ID twice should produce one reminder, got %d: %v", len(out), out)
	}
	if !strings.Contains(out[0], "new") {
		t.Errorf("second registration should win, got %q", out[0])
	}
}

func TestReminderProviderEmptyOutput(t *testing.T) {
	s := NewReminderQueue()
	s.Register("skills", func() string { return "" })
	s.Register("memory", func() string { return "stuff" })

	s.RequeueSystemReminders()
	out := s.Drain()
	if len(out) != 1 {
		t.Fatalf("empty provider output should be skipped, got %d reminders: %v", len(out), out)
	}
	if !strings.Contains(out[0], "stuff") {
		t.Errorf("non-empty provider should remain: %q", out[0])
	}
}

func TestReminderUnregister(t *testing.T) {
	s := NewReminderQueue()
	s.Register("a", func() string { return "alpha" })
	s.Register("b", func() string { return "beta" })

	s.Unregister("a")
	s.RequeueSystemReminders()

	out := s.Drain()
	if len(out) != 1 || !strings.Contains(out[0], "beta") {
		t.Errorf("Unregister should drop the named provider, got %v", out)
	}
}

// TestReminderFullSessionLifecycle simulates the harness flow end-to-end: a
// session starts, providers (skills + memory) get enqueued, the user types a
// message, attached reminders ride along; on PostCompact the same providers
// re-emit so the LLM doesn't lose context. This is what the model wires up
// across hooks.go (SessionStart), model.go (PostCompact), and agent.go
// (sendToAgent).
func TestReminderFullSessionLifecycle(t *testing.T) {
	s := NewReminderQueue()

	skillsBody := "Use the Skill tool to invoke these capabilities:\n\n- git: Git workflow"
	memoryBody := "<memory scope=\"user\">\nAlways use tabs.\n</memory>"

	s.Register("skills-directory", func() string { return skillsBody })
	s.Register("memory-user", func() string { return memoryBody })

	// SessionStart: harness enqueues all providers.
	s.RequeueSystemReminders()

	// User submits "hello"; sendToAgent drains and attaches.
	firstUserMsg := reminder.AppendBlocks(ai.TextContent("hello"), s.Drain()...).Text()
	if !strings.Contains(firstUserMsg, "hello") {
		t.Error("first user message should preserve the user's typed content")
	}
	if !strings.Contains(firstUserMsg, skillsBody) {
		t.Error("first user message should carry skills directory reminder")
	}
	if !strings.Contains(firstUserMsg, memoryBody) {
		t.Error("first user message should carry memory reminder")
	}
	if !strings.Contains(firstUserMsg, "<system-reminder") {
		t.Error("reminders must be wrapped in <system-reminder> (with or without source attr)")
	}

	// User submits "hello again"; queue empty after drain — no reminders.
	secondUserMsg := reminder.AppendBlocks(ai.TextContent("hello again"), s.Drain()...).Text()
	if secondUserMsg != "hello again" {
		t.Errorf("second message should have no reminders attached, got %q", secondUserMsg)
	}

	// PostCompact: history was replaced, so rearm and render on first input.
	s.ResetConversation()
	postCompactMsg := s.PrepareInput(core.UserMessage("after compact", nil)).Text()
	if !strings.Contains(postCompactMsg, skillsBody) {
		t.Error("post-compact message should re-attach skills reminder")
	}
	if !strings.Contains(postCompactMsg, memoryBody) {
		t.Error("post-compact message should re-attach memory reminder")
	}
}

// TestReminderProviderReflectsLatestState verifies that providers are queried
// on every emission, so changes between SessionStart and PostCompact (e.g.
// the user toggled a skill in the middle) are picked up.
func TestReminderProviderReflectsLatestState(t *testing.T) {
	s := NewReminderQueue()
	state := "v1"
	s.Register("skills", func() string { return state })

	s.RequeueSystemReminders()
	if got := s.Drain(); !strings.Contains(got[0], "v1") {
		t.Fatalf("first emission should reflect v1, got %v", got)
	}

	state = "v2"

	s.RequeueSystemReminders()
	if got := s.Drain(); !strings.Contains(got[0], "v2") {
		t.Errorf("second emission should reflect mutated state v2, got %v", got)
	}
}

// TestReminderRequeueSystemRemindersIsIdempotent guards against the slow-growing
// duplicate-emission leak: SessionStart → PostCompact → /skills toggle in
// close succession should produce one emission per provider, not three.
// One-time notices (Enqueue) must survive a re-emission unmolested.
func TestReminderRequeueSystemRemindersIsIdempotent(t *testing.T) {
	s := NewReminderQueue()
	s.Register("skills-directory", func() string { return "skills body" })
	s.Register("memory-user", func() string { return "user mem" })

	// Hook-context notice queued before the first emission.
	s.Enqueue("hook context A")

	s.RequeueSystemReminders() // first SessionStart
	s.RequeueSystemReminders() // /skills toggle
	s.RequeueSystemReminders() // PostCompact

	out := s.Drain()
	// 1 notice + 2 provider entries (one per provider) = 3 total.
	if len(out) != 3 {
		t.Fatalf("expected 3 reminders (1 notice + 2 providers), got %d: %v", len(out), out)
	}

	var skillsCount, memoryCount, hookCount int
	for _, r := range out {
		switch {
		case strings.Contains(r, "skills body"):
			skillsCount++
		case strings.Contains(r, "user mem"):
			memoryCount++
		case strings.Contains(r, "hook context A"):
			hookCount++
		}
	}
	if skillsCount != 1 || memoryCount != 1 || hookCount != 1 {
		t.Errorf("expected each reminder exactly once; got skills=%d memory=%d hook=%d",
			skillsCount, memoryCount, hookCount)
	}
}

func TestProviderVersionsAndRemoval(t *testing.T) {
	s := NewReminderQueue()
	state := "v1"
	s.Register("skills", func() string { return state })
	s.RequeueSystemReminders()
	s.Drain()

	state = "v2"
	s.RequeueSystemReminders()
	state = "v1"
	s.RequeueSystemReminders()
	if got := s.Drain(); len(got) != 0 {
		t.Fatalf("reverted pending version should not emit: %v", got)
	}

	state = " "
	s.RequeueSystemReminders()
	s.Enqueue("first notice")
	s.Enqueue("second notice")
	s.RequeueSystemReminders()
	removed := reminder.WrapWithSource(removalBody, "skills")
	if got := s.Drain(); len(got) != 3 || got[0] != reminder.Wrap("first notice") || got[1] != reminder.Wrap("second notice") || got[2] != removed {
		t.Fatalf("expected notices in order followed by scoped removal, got %v", got)
	}
	s.RequeueSystemReminders()
	if got := s.Drain(); len(got) != 0 {
		t.Fatalf("removal should only emit once: %v", got)
	}

	state = "v1"
	s.RequeueSystemReminders()
	if got := s.Drain(); len(got) != 1 || got[0] != reminder.WrapWithSource(state, "skills") {
		t.Fatalf("returning provider should emit again: %v", got)
	}
	s.Unregister("skills")
	s.RequeueSystemReminders()
	if got := s.Drain(); len(got) != 1 || got[0] != removed {
		t.Fatalf("unregistered provider should emit a removal: %v", got)
	}
}

func TestReminderConcurrentAccess(t *testing.T) {
	s := NewReminderQueue()

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			s.Enqueue("item")
		}(i)
	}
	wg.Wait()

	out := s.Drain()
	if len(out) != 50 {
		t.Errorf("expected 50 enqueued items, got %d", len(out))
	}
}

// Blocks recovers reminders from flattened text so /context can attribute them.
func TestBlocksRecoversAttachedReminders(t *testing.T) {
	s := NewReminderQueue()
	s.Register(reminder.ProviderSkillsDirectory, func() string { return "skills body" })
	s.Register(reminder.ProviderMemoryProject, func() string { return "project memory body" })
	s.RequeueSystemReminders()
	s.Enqueue("a one-time notice")

	content := reminder.AppendBlocks(ai.TextContent("what the user typed"), s.Drain()...).Text()

	blocks := reminder.Blocks(content)
	if len(blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d from:\n%s", len(blocks), content)
	}
	wantSources := []string{reminder.ProviderSkillsDirectory, reminder.ProviderMemoryProject, ""}
	for i, want := range wantSources {
		if blocks[i].Source != want {
			t.Errorf("block %d: source = %q, want %q", i, blocks[i].Source, want)
		}
		if !strings.HasPrefix(blocks[i].Text, "<system-reminder") ||
			!strings.HasSuffix(blocks[i].Text, "</system-reminder>") {
			t.Errorf("block %d text is not a whole reminder: %q", i, blocks[i].Text)
		}
	}
	if strings.Contains(blocks[0].Text, "project memory body") {
		t.Error("blocks ran together; the skills block swallowed the memory block")
	}
}

// Pending has to leave the queue intact — /context inspects what the next turn
// will carry, it does not consume it.
func TestPendingDoesNotDrain(t *testing.T) {
	s := NewReminderQueue()
	s.Enqueue("queued")

	if got := s.Pending(); len(got) != 1 {
		t.Fatalf("Pending returned %d entries, want 1", len(got))
	}
	if s.Empty() {
		t.Error("Pending consumed the queue")
	}
	if got := s.Drain(); len(got) != 1 {
		t.Errorf("Drain after Pending returned %d entries, want 1", len(got))
	}
}
