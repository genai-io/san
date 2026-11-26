package agent

import (
	"slices"
	"strings"

	"github.com/genai-io/san/internal/agent/reminder"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/sdk-go/pkg/ai"
)

// Prepared reports whether this conversation has run initial preparation, not delivery success.
func (s *ReminderQueue) Prepared() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prepared
}

// ResetConversation drops obsolete notices and rearms provider context after clear or compaction.
func (s *ReminderQueue) ResetConversation() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = nil
	s.emitted = nil
	s.prepared = false
}

// RestoreHistory primes dedup from the latest block per registered provider in
// active user messages. Historical text is never queued; PrepareInput rerenders.
func (s *ReminderQueue) RestoreHistory(messages []core.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prepared = false
	s.pending = slices.DeleteFunc(s.pending, func(e pendingEntry) bool { return e.providerID != "" })
	s.emitted = make(map[string]string, len(s.providers))
	known := make(map[string]bool, len(s.providers))
	for _, p := range s.providers {
		if p.id != "" {
			known[p.id] = true
		}
	}
	for _, msg := range messages {
		if msg.Role != ai.RoleUser {
			continue
		}
		for _, part := range msg.Content {
			if part.Type != ai.BlockText {
				continue
			}
			for _, block := range reminder.Blocks(part.Text) {
				if known[block.Source] {
					s.emitted[block.Source] = strings.Clone(block.Text)
				}
			}
		}
	}
}

// PrepareInput emits initial context once, then drains notices into separate text blocks.
// A rejected input must retain the returned message.
func (s *ReminderQueue) PrepareInput(msg core.Message) core.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.prepared {
		s.requeueSystemRemindersLocked()
		s.prepared = true
	}
	msg.Content = reminder.AppendBlocks(msg.Content, s.drainLocked()...)
	return msg
}
