package reminder

import "github.com/genai-io/san/internal/core"

// Prepared reports whether initial reminders were prepared or restored, not delivery success.
func (s *Reminder) Prepared() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prepared
}

// ResetConversation drops obsolete notices and rearms provider context after clear or compaction.
func (s *Reminder) ResetConversation() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.discardPendingNoticesLocked()
	s.prepared = false
}

// RestoreHistory recognizes provider context in the active native conversation.
func (s *Reminder) RestoreHistory(messages []core.ChatMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prepared = false
	for _, msg := range messages {
		if msg.Role == core.ChatUser && HasSystemReminder(msg.Content) {
			s.prepared = true
			break
		}
	}
}

// PrepareInput emits initial context once, then drains notices; rejected input must retain the returned text.
func (s *Reminder) PrepareInput(text string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.prepared {
		s.requeueSystemRemindersLocked()
		s.prepared = true
	}
	text = AttachToContent(text, s.wrappedLocked())
	s.pending = nil
	return text
}
