// Package task tracks background bash and subagent tasks for the TUI's
// task panel and the agent's TaskGet tool. Exposes *Supervisor directly.
package task

// Options holds all dependencies for initialization.
type Options struct {
	OutputDir string
}

// Initialize creates the package-level *Supervisor and configures it. It returns
// the error from configuring the output directory: the supervisor is installed
// either way, so a caller that cannot use the directory still gets a working
// in-memory supervisor and can decide what to say about the lost persistence.
func Initialize(opts Options) error {
	m := NewSupervisor()
	var err error
	if opts.OutputDir != "" {
		err = m.SetOutputDir(opts.OutputDir)
	}
	defaultSupervisor = m
	return err
}

// Default returns the package-level *Supervisor.
func Default() *Supervisor {
	return defaultSupervisor
}

// ResetDefault restores a fresh empty *Supervisor. Intended for
// tests.
func ResetDefault() {
	defaultSupervisor = NewSupervisor()
}

var defaultSupervisor = NewSupervisor()
