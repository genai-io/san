package transcript

import "time"

type Transcript struct {
	ID        string
	ParentID  string
	Cwd       string
	CreatedAt time.Time
	UpdatedAt time.Time

	Provider string
	Model    string

	Messages []Node
	State    State
}

type Node struct {
	ID          string
	ParentID    string
	Role        string
	Time        time.Time
	Cwd         string
	GitBranch   string
	AgentID     string
	IsSidechain bool
	Content     []ContentBlock
}

type State struct {
	Title      string
	LastPrompt string
	Tag        string
	Mode       string
	// AutoPilot is the session's autopilot config as a JSON blob (empty when
	// unset). Stored opaque here so the transcript package stays free of an
	// internal/setting dependency; the app marshals/unmarshals it.
	AutoPilot string

	Tasks    []TrackerItemView
	Worktree *WorktreeState
}

type TrackerItemView struct {
	ID              string
	Subject         string
	Description     string
	ActiveForm      string
	Status          string
	Owner           string
	Blocks          []string
	BlockedBy       []string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	StatusChangedAt time.Time
}

type ListItem struct {
	SessionID string
	FullPath  string
	CreatedAt time.Time
	UpdatedAt time.Time

	Title        string
	LastPrompt   string
	MessageCount int
	GitBranch    string

	IsSidechain bool
}
