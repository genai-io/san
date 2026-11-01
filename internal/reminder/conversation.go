package reminder

import "github.com/genai-io/san/internal/core"

// ContextPrepared reports whether initial provider context was prepared or restored.
// Preparation attaches context to pending input; it does not confirm successful delivery.
func (s *Reminder) ContextPrepared() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.contextPrepared
}

// ResetConversation drops obsolete notices and rearms provider context after clear or compaction.
func (s *Reminder) ResetConversation() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.discardPendingNoticesLocked()
	s.contextPrepared = false
}

// RestoreHistory recognizes provider context in the active native conversation.
func (s *Reminder) RestoreHistory(messages []core.ChatMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.contextPrepared = false
	for _, msg := range messages {
		if msg.Role == core.ChatUser && HasSystemReminder(msg.Content) {
			s.contextPrepared = true
			break
		}
	}
}

// PrepareInput emits initial context once, then drains notices; rejected input must retain the returned text.
func (s *Reminder) PrepareInput(text string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.contextPrepared {
		s.requeueSystemRemindersLocked()
		s.contextPrepared = true
	}
	text = AttachToContent(text, s.wrappedLocked())
	s.pending = nil
	return text
}
