// Package session owns per-run conversation state: session identity,
// transcript persistence, recorder, fork/load/save. The package exposes
// the concrete *Setup directly — no Service interface.
//
// Why no interface: consumers of session don't share a narrow common
// surface. The agent loop needs NewRecorder + ID; the app composition
// root needs Save / Load / LoadLatest / EnsureStore / SetID; the TUI
// session selector needs *Store. Each caller's slice is different. A
// producer-side union interface would just be *Setup with a rename;
// TEMPLATE Rule 3 says don't do that.
//
// If a future caller benefits from narrowing, declare a small
// consumer-defined interface in that caller's package. Until then,
// *Setup is the contract.
package session

import (
	"go.uber.org/zap"

	"github.com/genai-io/san/v2/internal/log"
)

// Options holds configuration for Initialize.
type Options struct {
	CWD string
}

// Initialize creates a session store and generates a fresh session ID.
// Installs the result on the package-level *Setup so Default() reflects
// the new state.
func Initialize(opts Options) {
	store, err := NewStore(opts.CWD)
	if err != nil {
		log.Logger().Warn("session store initialization failed, sessions will not be persisted", zap.Error(err))
	}

	defaultSetup.mu.Lock()
	defaultSetup.SessionID = NewSessionID()
	defaultSetup.Store = store
	defaultSetup.mu.Unlock()
}

// Default returns the package-level *Setup. Returns an empty (no-store,
// no-session-ID) *Setup until Initialize runs, so callers that touch it
// pre-init don't crash.
func Default() *Setup {
	return defaultSetup
}

// SetDefaultSetup replaces the package-level *Setup. Intended for
// tests. A nil argument restores a fresh empty *Setup.
func SetDefaultSetup(s *Setup) {
	if s == nil {
		defaultSetup = &Setup{}
		return
	}
	defaultSetup = s
}

// ResetDefaultSetup restores a fresh empty *Setup. Intended for tests.
func ResetDefaultSetup() {
	defaultSetup = &Setup{}
}
