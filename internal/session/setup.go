package session

import (
	"fmt"
	"sync"
)

// defaultSetup is the package-level session setup, initialized by Initialize().
var defaultSetup = &Setup{}

// Setup holds the initialized session infrastructure needed by the app layer.
type Setup struct {
	mu sync.RWMutex

	Store     *Store
	SessionID string
}

// EnsureStore lazily initializes the session store for the given cwd.
func (s *Setup) EnsureStore(cwd string) error {
	s.mu.RLock()
	if s.Store != nil {
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
	if s.Store == nil {
		s.Store = store
	}
	return nil
}

// ── Service interface implementation ──────────────────────

// ID returns the current session ID.
func (s *Setup) ID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.SessionID
}

// SetID updates the current session ID.
func (s *Setup) SetID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.SessionID = id
}

// TranscriptPath returns the transcript file path for the current session,
// or empty string if the store is nil.
func (s *Setup) TranscriptPath() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.Store != nil {
		return s.Store.SessionPath(s.SessionID)
	}
	return ""
}

// GetStore returns the underlying session store (may be nil).
// Named GetStore to avoid conflict with the exported Store field.
func (s *Setup) GetStore() *Store {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Store
}

// SetStore replaces the session store.
func (s *Setup) SetStore(st *Store) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Store = st
}

// Save persists a session snapshot via the store.
func (s *Setup) Save(snap *Snapshot) error {
	s.mu.RLock()
	st := s.Store
	s.mu.RUnlock()
	if st == nil {
		return fmt.Errorf("session store not initialized")
	}
	return st.Save(snap)
}

// Load loads a session by ID via the store.
func (s *Setup) Load(id string) (*Snapshot, error) {
	s.mu.RLock()
	st := s.Store
	s.mu.RUnlock()
	if st == nil {
		return nil, fmt.Errorf("session store not initialized")
	}
	return st.Load(id)
}

// LoadLatest loads the most recent session via the store.
func (s *Setup) LoadLatest() (*Snapshot, error) {
	s.mu.RLock()
	st := s.Store
	s.mu.RUnlock()
	if st == nil {
		return nil, fmt.Errorf("session store not initialized")
	}
	return st.GetLatest()
}

// List returns metadata for all sessions via the store.
func (s *Setup) List() ([]*SessionMetadata, error) {
	s.mu.RLock()
	st := s.Store
	s.mu.RUnlock()
	if st == nil {
		return nil, fmt.Errorf("session store not initialized")
	}
	return st.List()
}

// Fork forks a session by ID via the store.
func (s *Setup) Fork(id string) (*Snapshot, error) {
	s.mu.RLock()
	st := s.Store
	s.mu.RUnlock()
	if st == nil {
		return nil, fmt.Errorf("session store not initialized")
	}
	return st.Fork(id)
}

// NewRecorder binds a Recorder to the current session and transcript store.
// Returns nil if the store is not initialized — callers can pass the nil
// result through to core.Config.OnEvent safely because Recorder.OnAgentEvent
// is nil-safe.
func (s *Setup) NewRecorder(agentID, provider, model string, maxTokens int) *Recorder {
	s.mu.RLock()
	st := s.Store
	sessionID := s.SessionID
	s.mu.RUnlock()
	if st == nil || st.transcriptStore == nil || sessionID == "" {
		return nil
	}
	return NewRecorder(RecorderOptions{
		FileStore: st.transcriptStore,
		SessionID: sessionID,
		AgentID:   agentID,
		Provider:  provider,
		Model:     model,
		MaxTokens: maxTokens,
	})
}
