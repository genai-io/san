package transcript

import "time"

type StartCommand struct {
	SessionID       string
	Cwd             string
	Provider        string
	Model           string
	MaxTokens       int
	AgentID         string
	ParentSessionID string
	Time            time.Time
}

type ForkCommand struct {
	SourceSessionID string
	NewSessionID    string
	Time            time.Time
}

type ListOptions struct {
	Limit            int
	IncludeSidechain bool
}
