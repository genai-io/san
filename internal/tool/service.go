// Package tool is the registry of built-in tools the agent can call.
// Exposes *Registry directly — no Service interface.
package tool

// Default returns the package-level *Registry.
func Default() *Registry {
	return defaultRegistry
}
