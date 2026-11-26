package setting

// RunOptions configures the interactive host.
type RunOptions struct {
	Prompt    string // initial prompt for interactive TUI
	PluginDir string
	Persona   string // persona name to activate on startup
	Continue  bool   // resume most recent session
	Resume    bool   // open session selector or resume by ID
	ResumeID  string // specific session ID to resume
	Version   string // build version of the running binary, for the background auto-update
}
