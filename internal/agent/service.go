// Package agent owns the foreground agent session lifecycle. *Session
// is the concrete handle; the package exposes it directly.
package agent

// Options holds dependencies for initialization.
type Options struct{}

// Initialize installs a fresh *Session as the package-level default.
func Initialize(opts Options) {
	defaultSession = &Session{}
}

// Default returns the package-level *Session.
func Default() *Session {
	return defaultSession
}

// SetDefaultSession replaces the package-level *Session. Intended for
// tests. A nil argument restores a fresh empty *Session.
func SetDefaultSession(s *Session) {
	if s == nil {
		defaultSession = &Session{}
		return
	}
	defaultSession = s
}

// ResetDefaultSession restores a fresh empty *Session. Intended for
// tests.
func ResetDefaultSession() {
	defaultSession = &Session{}
}

var defaultSession = &Session{}
