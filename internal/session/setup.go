package session

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// defaultSetup is the package-level session setup, initialized by Initialize().
var defaultSetup = &Setup{}

// Setup holds the initialized session infrastructure needed by the app layer.
type Setup struct {
	mu sync.RWMutex

	Store     *Store
	SessionID string

	// recorder caches the main-session Recorder so the app layer can emit
	// audit events (permission decisions, skill state changes, etc.) from
	// code paths that don't have a direct handle to the agent build site.
	// First NewRecorder call wins; subsequent calls return the cached value
	// if it still matches the session.
	recorder *Recorder
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
//
// Caches the result so subsequent callers (e.g. permission decision sites
// far from the agent build) can pull the same recorder via Recorder().
func (s *Setup) NewRecorder(agentID, provider, model string, maxTokens int) *Recorder {
	// Fast path: cache hit under read lock.
	s.mu.RLock()
	if s.recorder != nil && s.recorder.sessionID == s.SessionID && s.Store != nil {
		cached := s.recorder
		s.mu.RUnlock()
		return cached
	}
	st := s.Store
	sessionID := s.SessionID
	s.mu.RUnlock()

	if st == nil || st.transcriptStore == nil || sessionID == "" {
		return nil
	}

	// Resolve the leaf BEFORE taking the write lock — the Load can be MB-scale
	// for resumed sessions and we don't want to block other Setup operations
	// on disk I/O. Stat-gate so fresh sessions skip Load entirely.
	leaf := loadLeafIfExists(st, sessionID)

	s.mu.Lock()
	defer s.mu.Unlock()
	// Re-check after re-acquiring: another caller may have raced ahead.
	if s.recorder != nil && s.recorder.sessionID == sessionID {
		return s.recorder
	}
	r := NewRecorder(RecorderOptions{
		FileStore: st.transcriptStore,
		SessionID: sessionID,
		AgentID:   agentID,
		Provider:  provider,
		Model:     model,
		MaxTokens: maxTokens,
		Cwd:       st.cwd,
		ProjectID: st.projectID,
	})
	if leaf != "" {
		r.seedLastMessageID(leaf)
	}
	s.recorder = r
	return r
}

// loadLeafIfExists returns the active-chain leaf message ID of an existing
// transcript so the recorder's parent pointer can chain new messages off the
// loaded history. Errors are swallowed — bad transcript just means no seed.
func loadLeafIfExists(st *Store, sessionID string) string {
	if st == nil || st.transcriptStore == nil || sessionID == "" {
		return ""
	}
	leaf, _ := st.transcriptStore.LastMessageID(context.Background(), sessionID)
	return leaf
}

// NewSidechainRecorder returns a fresh Recorder bound to a NEW session
// that is its own resumable transcript ("san --resume <id>" replays
// the fork in isolation), parented under the live main session via
// agentID so the inspector can still associate them. Each fork should
// call this once and pass the result as the fork agent's
// core.Config.OnEvent. The recorder is not cached because there can be
// many concurrent forks of different kinds.
//
// The fork session ID has the form
//
//	<main-session>.<agentID>.<unix-seconds>
//
// which keeps the parent-child relationship readable, lets multiple
// forks of the same kind coexist (different timestamps), and stays a
// valid SessionID for the resume path.
//
// Returns nil when the session store isn't ready — the L1 fork is
// best-effort so a missing recorder is fine.
func (s *Setup) NewSidechainRecorder(agentID, provider, model string, maxTokens int) *Recorder {
	s.mu.RLock()
	st := s.Store
	parentSessionID := s.SessionID
	s.mu.RUnlock()

	if st == nil || st.transcriptStore == nil || parentSessionID == "" {
		return nil
	}
	forkSessionID := fmt.Sprintf("%s.%s.%d", parentSessionID, agentID, time.Now().Unix())
	return NewRecorder(RecorderOptions{
		FileStore: st.transcriptStore,
		SessionID: forkSessionID,
		AgentID:   agentID,
		Provider:  provider,
		Model:     model,
		MaxTokens: maxTokens,
		Cwd:       st.cwd,
		ProjectID: st.projectID,
		// Sidechain flag is what the inspector reads to keep fork messages
		// out of the main thread; without it the L1 review would pollute
		// the main chain.
		Sidechain: true,
	})
}

// Recorder returns the cached main-session Recorder, or nil if NewRecorder
// has not been called for the current session. Audit emit sites outside the
// agent build path use this to surface events without holding their own
// reference.
func (s *Setup) Recorder() *Recorder {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.recorder == nil || s.recorder.sessionID != s.SessionID {
		return nil
	}
	return s.recorder
}
