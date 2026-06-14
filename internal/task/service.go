// Package task tracks background bash and subagent tasks for the TUI's
// task panel and the agent's TaskOutput / TaskList tools. Exposes
// *Manager directly.
package task

// Options holds all dependencies for initialization.
type Options struct {
	OutputDir string
}

// Initialize creates the package-level *Manager and configures it.
func Initialize(opts Options) {
	m := NewManager()
	if opts.OutputDir != "" {
		m.SetOutputDir(opts.OutputDir)
	}
	defaultManager = m
}

// Default returns the package-level *Manager.
func Default() *Manager {
	return defaultManager
}

// SetDefaultTracker replaces the package-level *Manager. Intended for
// tests. A nil argument restores a fresh empty *Manager.
func SetDefaultTracker(m *Manager) {
	if m == nil {
		defaultManager = NewManager()
		return
	}
	defaultManager = m
}

// ResetDefaultTracker restores a fresh empty *Manager. Intended for
// tests.
func ResetDefaultTracker() {
	defaultManager = NewManager()
}

var defaultManager = NewManager()

// SetOutputDir on *Manager delegates to the package-level setOutputDir.
func (m *Manager) SetOutputDir(dir string) error {
	return setOutputDir(dir)
}
