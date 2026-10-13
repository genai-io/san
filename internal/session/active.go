package session

import (
	"fmt"
	"os"
	"sync"
)

// defaultActive is the process session, initialized by Initialize().
var defaultActive = &Active{}

// Active holds the initialized session infrastructure needed by the app layer.
type Active struct {
	mu sync.RWMutex

	store *Store
	id    string

	// writer caches the main-session Writer so the app layer can emit
	// audit events (permission decisions, skill state changes, etc.) from
	// code paths that don't have a direct handle to the agent build site.
	// First NewWriter call wins; subsequent calls return the cached value
	// if it still matches the session.
	writer *Writer
}

func NewActive(store *Store, id string) *Active {
	return &Active{store: store, id: id}
}

// EnsureStore lazily initializes the session store for the given cwd.
func (s *Active) EnsureStore(cwd string) error {
	s.mu.RLock()
	if s.store != nil {
		s.mu.RUnlock()
		return nil
	}
	s.mu.RUnlock()

	store, err := NewStore(cwd)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store == nil {
		s.store = store
	}
	return nil
}

// ID returns the current session ID.
func (s *Active) ID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.id
}

// SetID updates the current session ID, retiring the previous session's writer.
func (s *Active) SetID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.id == id {
		return
	}
	if s.writer != nil {
		s.writer.Close()
	}
	s.id = id
	s.writer = nil
}

// TranscriptPath returns the transcript file path for the current session,
// or empty string if the store is nil.
func (s *Active) TranscriptPath() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.store != nil {
		return s.store.SessionPath(s.id)
	}
	return ""
}

// Store returns the underlying session store (may be nil).
func (s *Active) Store() *Store {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.store
}

// Save persists a session snapshot via the store.
func (s *Active) Save(snap *Snapshot) error {
	s.mu.RLock()
	st, rec := s.store, s.writer
	s.mu.RUnlock()
	if st == nil {
		return fmt.Errorf("session store not initialized")
	}
	if rec != nil && rec.sessionID == snap.Metadata.ID {
		if err := rec.Err(); err != nil {
			return err
		}
		return st.SaveState(snap)
	}
	return st.Save(snap)
}

func (s *Active) Flush() error {
	s.mu.RLock()
	st := s.store
	s.mu.RUnlock()
	if st == nil {
		return nil
	}
	return st.Flush()
}

// Resume owns the writer lease before reading the native conversation. Holding
// the lease while idle prevents another process from invalidating that seed.
// Failed resumes leave the current session intact.
func (s *Active) Resume(id string) (*Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store == nil {
		return nil, fmt.Errorf("session store not initialized")
	}
	st := s.store
	if _, err := os.Stat(st.SessionPath(id)); err != nil {
		return nil, fmt.Errorf("resume session %s: %w", id, err)
	}
	if s.id == id && s.writer != nil {
		if s.writer.Err() == nil && !s.writer.isClosed() {
			return st.Load(id)
		}
		s.writer.Close()
		s.writer = nil
	}
	w := NewWriter(WriterOptions{Store: st.transcriptStore, SessionID: id, AgentID: "main", Cwd: st.cwd})
	if err := w.Err(); err != nil {
		w.Close()
		return nil, err
	}
	snapshot, err := st.Load(id)
	if err != nil {
		w.Close()
		return nil, err
	}
	if s.writer != nil {
		s.writer.Close()
	}
	s.id, s.writer = id, w
	return snapshot, nil
}

// ResumeLatest adopts the most recent session under its writer lease.
func (s *Active) ResumeLatest() (*Snapshot, error) {
	s.mu.RLock()
	st := s.store
	s.mu.RUnlock()
	if st == nil {
		return nil, fmt.Errorf("session store not initialized")
	}
	items, err := st.List()
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("no saved sessions")
	}
	return s.Resume(items[0].ID)
}

// Fork forks a session by ID via the store.
func (s *Active) Fork(id string) (*Snapshot, error) {
	s.mu.RLock()
	st := s.store
	s.mu.RUnlock()
	if st == nil {
		return nil, fmt.Errorf("session store not initialized")
	}
	return st.Fork(id)
}

// NewWriter binds a Writer to the current session and transcript store.
// Returns nil if the store is not initialized — callers can pass the nil
// result through to core.Config.OnEvent safely because Writer.OnAgentEvent
// is nil-safe.
//
// Caches the result so subsequent callers (e.g. permission decision sites
// far from the agent build) can pull the same writer via Writer().
func (s *Active) NewWriter(agentID, provider, model string, maxTokens int) *Writer {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writer != nil {
		return s.writer
	}
	if s.store == nil || s.id == "" {
		return nil
	}
	st := s.store
	s.writer = NewWriter(WriterOptions{Store: st.transcriptStore, SessionID: s.id, AgentID: agentID, Provider: provider, Model: model, MaxTokens: maxTokens, Cwd: st.cwd})
	return s.writer
}

// NewChildWriter creates an independent transcript with an explicit parent session ID.
func (s *Active) NewChildWriter(agentID, provider, model string, maxTokens int) *Writer {
	s.mu.RLock()
	st := s.store
	parentSessionID := s.id
	s.mu.RUnlock()

	if st == nil || st.transcriptStore == nil || parentSessionID == "" {
		return nil
	}
	forkSessionID := NewSessionID()
	return NewWriter(WriterOptions{
		Store:           st.transcriptStore,
		SessionID:       forkSessionID,
		AgentID:         agentID,
		Provider:        provider,
		Model:           model,
		MaxTokens:       maxTokens,
		Cwd:             st.cwd,
		ParentSessionID: parentSessionID,
		// Sidechain flag is what the inspector reads to keep fork messages
		// out of the main thread; without it the L1 review would pollute
		// the main chain.
		Sidechain: true,
	})
}

// Writer returns the cached main-session Writer, or nil if NewWriter
// has not been called for the current session. Audit emit sites outside the
// agent build path use this to surface events without holding their own
// reference.
func (s *Active) Writer() *Writer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.writer == nil || s.writer.sessionID != s.id {
		return nil
	}
	return s.writer
}

// Close releases the current writer and flushes derived summaries.
func (s *Active) Close() error {
	s.mu.Lock()
	if s.writer != nil {
		s.writer.Close()
		s.writer = nil
	}
	st := s.store
	s.mu.Unlock()
	if st == nil {
		return nil
	}
	return st.Flush()
}
