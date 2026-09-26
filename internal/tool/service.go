// Package tool is the registry of built-in tools the agent can call.
// Exposes *Registry directly — no Service interface.
package tool

// Initialize installs the package-level *Registry as the default.
// Tools register at init time before Initialize runs; this is a
// no-op trigger for consistency with other packages' Initialize.
func Initialize() {
	defaultInstance = defaultRegistry
}

// Default returns the package-level *Registry.
func Default() *Registry {
	if defaultInstance != nil {
		return defaultInstance
	}
	return defaultRegistry
}

// ResetDefaultRegistry restores the package default. Intended for
// tests.
func ResetDefaultRegistry() {
	defaultInstance = defaultRegistry
}

var defaultInstance *Registry
