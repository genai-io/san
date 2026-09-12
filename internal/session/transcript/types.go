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

// TrackerItemView is the persisted shape of a todo.Item. It mirrors that struct
// field for field so the session layer converts by plain struct conversion,
// and carries the same JSON tags so PatchTasks payloads keep their wire form.
type TrackerItemView struct {
	ID              string         `json:"id"`
	Subject         string         `json:"subject"`
	Description     string         `json:"description"`
	ActiveForm      string         `json:"activeForm,omitempty"`
	Status          string         `json:"status"`
	Owner           string         `json:"owner,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	Blocks          []string       `json:"blocks"`
	BlockedBy       []string       `json:"blockedBy"`
	CreatedAt       time.Time      `json:"createdAt"`
	UpdatedAt       time.Time      `json:"updatedAt"`
	StatusChangedAt time.Time      `json:"statusChangedAt"`
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
