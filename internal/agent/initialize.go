// Package agent owns the foreground agent session lifecycle. *Runtime
// is the concrete handle; the package exposes it directly.
package agent

// Initialize installs a fresh *Runtime as the package-level default.
func Initialize() {
	defaultRuntime = &Runtime{}
}

// Default returns the package-level *Runtime.
func Default() *Runtime {
	return defaultRuntime
}

var defaultRuntime = &Runtime{}
