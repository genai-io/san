// Package agent owns the foreground agent session lifecycle. *Session
// is the concrete handle; the package exposes it directly.
package agent

// Initialize installs a fresh *Session as the package-level default.
func Initialize() {
	defaultSession = &Session{}
}

// Default returns the package-level *Session.
func Default() *Session {
	return defaultSession
}

var defaultSession = &Session{}
