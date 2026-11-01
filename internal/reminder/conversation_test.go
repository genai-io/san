package reminder_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/reminder"
)

func TestConversationContextLifecycle(t *testing.T) {
	s := reminder.New()
	body := "initial skills"
	s.Register(reminder.ProviderSkillsDirectory, func() string { return body })
	s.Enqueue("hook context")
	first := s.PrepareInput("first")
	if !s.Prepared() || !strings.Contains(first, body) || !strings.Contains(first, "hook context") {
		t.Fatalf("first input = %q", first)
	}
	if got := s.PrepareInput("second"); got != "second" {
		t.Fatalf("second input = %q", got)
	}
	body = "updated skills"
	s.RequeueSystemReminders()
	if got := s.PrepareInput("reload"); !strings.Contains(got, body) {
		t.Fatalf("reloaded input = %q", got)
	}
	s.Enqueue("obsolete notice")
	s.ResetConversation()
	if s.Prepared() {
		t.Fatal("reset retained context state")
	}
	body = "post compact skills"
	if got := s.PrepareInput("after compact"); !strings.Contains(got, body) || strings.Contains(got, "obsolete notice") {
		t.Fatalf("reset input = %q", got)
	}
}

func TestRestoreConversationContext(t *testing.T) {
	for _, tc := range []struct {
		name     string
		messages []core.ChatMessage
		want     bool
	}{
		{"empty", nil, false},
		{"notice", []core.ChatMessage{{Role: core.ChatUser, Content: reminder.Wrap("notice")}}, false},
		{"assistant", []core.ChatMessage{{Role: core.ChatAssistant, Content: reminder.WrapWithSource("skills", reminder.ProviderSkillsDirectory)}}, false},
		{"user", []core.ChatMessage{{Role: core.ChatUser, Content: reminder.WrapWithSource("skills", reminder.ProviderSkillsDirectory)}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := reminder.New()
			s.Register(reminder.ProviderSkillsDirectory, func() string { return "fresh skills" })
			s.PrepareInput("previous conversation")
			s.RestoreHistory(tc.messages)
			if s.Prepared() != tc.want {
				t.Fatalf("Prepared = %v", s.Prepared())
			}
			if got := s.PrepareInput("next"); strings.Contains(got, "fresh skills") == tc.want {
				t.Fatalf("restored input = %q", got)
			}
		})
	}
}

func TestRejectedInputRetainsReminders(t *testing.T) {
	s := reminder.New()
	s.Register(reminder.ProviderSkillsDirectory, func() string { return "provider body" })
	s.Enqueue("first notice")
	var q agent.InputQueue
	q.Enqueue(agent.PendingInput{Message: core.UserMessage("question", nil), FromUser: true})
	prepare := func(msg core.Message) core.Message {
		prepared := core.UserMessage(s.PrepareInput(msg.Text()), nil)
		prepared.ID = msg.ID
		return prepared
	}
	rejected := errors.New("rejected")
	if err := q.Deliver(func(core.Message) error { return rejected }, prepare, func(string) {}); !errors.Is(err, rejected) {
		t.Fatalf("Deliver = %v", err)
	}
	s.Enqueue("later notice")
	var got string
	if err := q.Deliver(func(msg core.Message) error { got = msg.Text(); return nil }, prepare, func(string) {}); err != nil {
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
	s := reminder.New()
	s.Register(reminder.ProviderSkillsDirectory, func() string { return "provider body" })
	results := make(chan string, 20)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() { results <- s.PrepareInput("input") })
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
