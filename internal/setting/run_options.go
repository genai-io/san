// Package options defines configuration types shared across app and tui packages.
package setting

// RunOptions contains all options for running the application.
type RunOptions struct {
	Print     string // non-empty → non-interactive print mode
	Prompt    string // initial prompt for interactive TUI
	PluginDir string
	Persona   string // persona name to activate on startup
	Continue  bool   // resume most recent session
	Resume    bool   // open session selector or resume by ID
	ResumeID  string // specific session ID to resume
}
