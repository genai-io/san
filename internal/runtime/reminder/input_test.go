package reminder_test

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/runtime"
	"github.com/genai-io/san/internal/runtime/reminder"
)

func prepareText(s *reminder.Queue, text string) string {
	return s.PrepareInput(core.UserMessage(text, nil)).Text()
}

func TestConversationContextLifecycle(t *testing.T) {
	s := reminder.NewQueue()
	body := "initial skills"
	s.Register(reminder.ProviderSkillsDirectory, func() string { return body })
	s.Enqueue("hook context")
	first := prepareText(s, "first")
	if !s.Prepared() || !strings.Contains(first, body) || !strings.Contains(first, "hook context") {
		t.Fatalf("first input = %q", first)
	}
	if got := prepareText(s, "second"); got != "second" {
		t.Fatalf("second input = %q", got)
	}
	body = "updated skills"
	s.RequeueSystemReminders()
	if got := prepareText(s, "reload"); !strings.Contains(got, body) {
		t.Fatalf("reloaded input = %q", got)
	}
	s.Enqueue("obsolete notice")
	s.ResetConversation()
	if s.Prepared() {
		t.Fatal("reset retained context state")
	}
	body = "post compact skills"
	if got := prepareText(s, "after compact"); !strings.Contains(got, body) || strings.Contains(got, "obsolete notice") {
		t.Fatalf("reset input = %q", got)
	}
}

func TestRestoreConversationContext(t *testing.T) {
	const skills, memory = reminder.ProviderSkillsDirectory, reminder.ProviderMemoryUser
	current := reminder.WrapWithSource("fresh skills", skills)
	stale := reminder.WrapWithSource("stale skills", skills)
	user := func(text string) core.Message { return core.UserMessage(text, nil) }
	summary := user("summary")
	summary.Content = reminder.AppendBlocks(summary.Content, current)
	for _, tc := range []struct {
		name     string
		messages []core.Message
		want     []string
	}{
		{"empty", nil, []string{skills, memory}},
		{"notice", []core.Message{user(reminder.Wrap("notice"))}, []string{skills, memory}},
		{"assistant", []core.Message{core.AssistantMessage(current, "", nil)}, []string{skills, memory}},
		{"unknown source", []core.Message{user(reminder.WrapWithSource("fresh skills", "unknown"))}, []string{skills, memory}},
		{"latest version wins", []core.Message{user(stale), user(current)}, []string{memory}},
		{"changed version rerenders", []core.Message{user(current), user(stale)}, []string{skills, memory}},
		{"summary carries provider separately", []core.Message{summary}, []string{memory}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := reminder.NewQueue()
			s.Register(skills, func() string { return "fresh skills" })
			s.Register(memory, func() string { return "fresh memory" })
			prepareText(s, "previous conversation")
			s.RestoreHistory(tc.messages)
			if s.Prepared() {
				t.Fatal("restore did not rearm rendering")
			}
			got := prepareText(s, "next")
			var sources []string
			for _, block := range reminder.Blocks(got) {
				sources = append(sources, block.Source)
				if block.Source == skills && block.Text != current {
					t.Fatalf("skills were not freshly rendered: %q", block.Text)
				}
			}
			if !slices.Equal(sources, tc.want) {
				t.Fatalf("restored input = %q; want sources %v", got, tc.want)
			}
		})
	}
}

func TestRejectedInputRetainsReminders(t *testing.T) {
	s := reminder.NewQueue()
	s.Register(reminder.ProviderSkillsDirectory, func() string { return "provider body" })
	s.Enqueue("first notice")
	var q runtime.InputQueue
	q.Enqueue(runtime.PendingInput{Message: core.UserMessage("question", nil), FromUser: true})
	prepare := s.PrepareInput
	rejected := errors.New("rejected")
	if err := q.DeliverFirst(func(runtime.PendingInput) error { return rejected }, prepare); !errors.Is(err, rejected) {
		t.Fatalf("DeliverFirst = %v", err)
	}
	s.Enqueue("later notice")
	s.RequeueSystemReminders()
	var got string
	if err := q.DeliverFirst(func(msg runtime.PendingInput) error { got = msg.Text(); return nil }, prepare); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"question", "provider body", "first notice", "later notice"} {
		if strings.Count(got, text) != 1 {
			t.Fatalf("retry input = %q; want %q once", got, text)
		}
	}
	if q.Len() != 0 || !s.Empty() {
		t.Fatal("successful retry left pending input or reminders")
	}
}

func TestConcurrentInputPreparesContextOnce(t *testing.T) {
	s := reminder.NewQueue()
	s.Register(reminder.ProviderSkillsDirectory, func() string { return "provider body" })
	results := make(chan string, 20)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() { results <- prepareText(s, "input") })
	}
	wg.Wait()
	close(results)
	count := 0
	for text := range results {
		if reminder.HasSystemReminder(text) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("context emitted %d times", count)
	}
}
