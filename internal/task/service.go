// Package task tracks background bash and subagent tasks for the TUI's
// task panel and the agent's TaskGet tool. Exposes *RunSet directly.
package task

// Options holds all dependencies for initialization.
type Options struct {
	OutputDir string
}

// Initialize creates the package-level *RunSet and configures it. It returns
// the error from configuring the output directory: the run set is installed
// either way, so a caller that cannot use the directory still gets a working
// in-memory run set and can decide what to say about the lost persistence.
func Initialize(opts Options) error {
	m := NewRunSet()
	var err error
	if opts.OutputDir != "" {
		err = m.SetOutputDir(opts.OutputDir)
	}
	defaultRunSet = m
	return err
}

// Default returns the package-level *RunSet.
func Default() *RunSet {
	return defaultRunSet
}

// ResetDefault restores a fresh empty *RunSet. Intended for
// tests.
func ResetDefault() {
	defaultRunSet = NewRunSet()
}

var defaultRunSet = NewRunSet()
