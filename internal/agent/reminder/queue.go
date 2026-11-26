package reminder

import (
	"slices"
	"sync"
)

// provider is a long-lived reminder source: render produces its body on every
// emission, and "" skips it (e.g. no enabled skills).
type provider struct {
	id     string
	render func() string
}

// Queue holds the runtime state for reminders.
//
// It tracks conversation context preparation and two reminder collections:
//
//   - providers: long-lived sources that emit once per conversation and
//     again when their content changes (e.g. skills, memory).
//   - pending: reminders queued for the next user message; each entry tracks
//     which provider (if any) emitted it so RequeueSystemReminders can replace
//     stale provider entries instead of duplicating them. Entries with no
//     provider are one-time notices (see Enqueue).
//
// All operations are safe for concurrent use.
type Queue struct {
	mu        sync.Mutex
	providers []provider
	pending   []pendingEntry
	emitted   map[string]string // latest wrapped version per provider
	prepared  bool
}

// pendingEntry pairs a wrapped <system-reminder> body with the ID of the
// provider that produced it. providerID is empty for one-time notices queued
// via Enqueue (e.g. hook context), so they always coexist with — and never
// shadow — provider emissions.
type pendingEntry struct {
	providerID string // "" for a one-time notice (Enqueue)
	wrapped    string // already wrapped in <system-reminder>
}

// NewQueue creates an empty Queue.
func NewQueue() *Queue {
	return &Queue{}
}

// Register adds a provider whose output RequeueSystemReminders emits.
// Re-registering an existing id replaces the old provider.
func (s *Queue) Register(id string, render func() string) {
	p := provider{id: id, render: render}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.providers {
		if existing.id == p.id {
			s.providers[i] = p
			return
		}
	}
	s.providers = append(s.providers, p)
}

// Unregister removes the provider with the given ID. Missing IDs are silently
// ignored.
func (s *Queue) Unregister(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.providers {
		if existing.id == id {
			s.providers = slices.Delete(s.providers, i, i+1)
			s.pending = slices.DeleteFunc(s.pending, func(e pendingEntry) bool {
				return e.providerID != "" && e.providerID == id
			})
			s.queueProviderLocked(id, "")
			return
		}
	}
}

// Enqueue adds a one-time notice to the pending queue. A notice is a reminder
// not backed by a provider — e.g. hook context — emitted once and never
// auto-regenerated. The body should not include the <system-reminder>
// wrapper; this method adds it.
//
// Empty bodies are dropped silently. Notices persist independently of
// RequeueSystemReminders — the latter only touches provider-emitted entries.
func (s *Queue) Enqueue(body string) {
	s.EnqueueWithSource(body, "")
}

// EnqueueWithSource queues one-time context with a source for inspection.
func (s *Queue) EnqueueWithSource(body, source string) {
	wrapped := WrapWithSource(body, source)
	if wrapped == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = append(s.pending, pendingEntry{wrapped: wrapped})
}

// DiscardPendingNotices drops every pending notice — entries queued via
// Enqueue (providerID==""). Used by /compact, /clear and /resume: the output
// that produced these notices is no longer in the conversation the model
// sees. Provider entries are preserved.
func (s *Queue) DiscardPendingNotices() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.discardPendingNoticesLocked()
}

func (s *Queue) discardPendingNoticesLocked() {
	if len(s.pending) == 0 {
		return
	}
	kept := s.pending[:0]
	for _, e := range s.pending {
		if e.providerID != "" {
			kept = append(kept, e)
		}
	}
	s.pending = kept
}

// RequeueSystemReminders replaces pending provider versions with current
// renders, skipping exact versions already attached. Notices keep their order.
// A provider that stops rendering content emits a source-scoped removal once.
func (s *Queue) RequeueSystemReminders() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requeueSystemRemindersLocked()
}

func (s *Queue) requeueSystemRemindersLocked() {
	if len(s.providers) == 0 {
		return
	}

	knownIDs := make(map[string]struct{}, len(s.providers))
	for _, p := range s.providers {
		knownIDs[p.id] = struct{}{}
	}
	kept := s.pending[:0]
	for _, e := range s.pending {
		if _, isProvider := knownIDs[e.providerID]; e.providerID == "" || !isProvider {
			kept = append(kept, e)
		}
	}
	s.pending = kept

	for _, p := range s.providers {
		s.queueProviderLocked(p.id, p.render())
	}
}

const removalBody = "Earlier reminders from this source no longer apply."

func (s *Queue) queueProviderLocked(id, body string) {
	wrapped := WrapWithSource(body, id)
	last := s.emitted[id]
	if wrapped == "" && last != "" {
		wrapped = WrapWithSource(removalBody, id)
	}
	if wrapped != "" && wrapped != last {
		s.pending = append(s.pending, pendingEntry{providerID: id, wrapped: wrapped})
	}
}

// Drain returns all pending reminders (already wrapped) and clears the queue.
func (s *Queue) Drain() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.drainLocked()
}

func (s *Queue) drainLocked() []string {
	out := s.wrappedLocked()
	for _, e := range s.pending {
		if e.providerID != "" {
			if s.emitted == nil {
				s.emitted = make(map[string]string)
			}
			s.emitted[e.providerID] = e.wrapped
		}
	}
	s.pending = nil
	return out
}

// Pending returns the queued reminders (already wrapped) without clearing the
// queue. Use it to inspect what the next user message will carry; use Drain
// when you are the one attaching them.
func (s *Queue) Pending() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wrappedLocked()
}

// wrappedLocked returns the queue's wire strings. Shared by Drain and Pending
// so the two views of the same queue cannot disagree about what an entry
// renders as. Caller holds s.mu.
func (s *Queue) wrappedLocked() []string {
	if len(s.pending) == 0 {
		return nil
	}
	out := make([]string, len(s.pending))
	for i, e := range s.pending {
		out[i] = e.wrapped
	}
	return out
}

// Empty reports whether there are no pending reminders.
func (s *Queue) Empty() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending) == 0
}
