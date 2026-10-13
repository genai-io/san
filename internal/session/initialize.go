// Package session owns durable session identity, product state, and runtime event writing.
package session

import (
	"go.uber.org/zap"

	"github.com/genai-io/san/internal/log"
)

// Options holds configuration for Initialize.
type Options struct {
	CWD string
}

// Initialize creates a session store and generates a fresh session ID.
// Installs the result on the package-level *Active so Default() reflects
// the new state.
func Initialize(opts Options) {
	store, err := NewStore(opts.CWD)
	if err != nil {
		log.Logger().Warn("session store initialization failed, sessions will not be persisted", zap.Error(err))
	}

	_ = defaultActive.Close()
	defaultActive.mu.Lock()
	defaultActive.id = NewSessionID()
	defaultActive.store = store
	defaultActive.mu.Unlock()
}

// Default returns the package-level *Active. Returns an empty (no-store,
// no-session-ID) *Active until Initialize runs, so callers that touch it
// pre-init don't crash.
func Default() *Active {
	return defaultActive
}
