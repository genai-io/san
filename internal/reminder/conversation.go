package reminder

import "github.com/genai-io/san/internal/core"

// ContextSent reports whether provider context has been prepared in this conversation.
func (s *Service) ContextSent() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.contextSent
}

// ResetConversation drops obsolete notices and rearms provider context after clear or compaction.
func (s *Service) ResetConversation() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.discardPendingNoticesLocked()
	s.contextSent = false
}

// RestoreHistory recognizes provider context in the active native conversation.
func (s *Service) RestoreHistory(messages []core.ChatMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.contextSent = false
	for _, msg := range messages {
		if msg.Role == core.ChatUser && HasSystemReminder(msg.Content) {
			s.contextSent = true
			break
		}
	}
}

// PrepareInput emits initial context once, then drains notices; rejected input must retain the returned text.
func (s *Service) PrepareInput(text string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.contextSent {
		s.requeueSystemRemindersLocked()
		s.contextSent = true
	}
	text = AttachToContent(text, s.wrappedLocked())
	s.pending = nil
	return text
}
