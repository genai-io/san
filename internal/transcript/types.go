package transcript

import (
	"time"

	"github.com/genai-io/sdk-go/pkg/ai"
)

type Transcript struct {
	ID              string
	ParentSessionID string
	Cwd             string
	CreatedAt       time.Time
	UpdatedAt       time.Time

	Provider string
	Model    string

	Messages    []MessageNode
	Notices     []NoticeRecord
	ToolDetails map[string]ToolDetailsRecord
	State       State
}

// MessageNode adds transcript position to the unmodified SDK message.
type MessageNode struct {
	ai.Message
	ParentMessageID string
	Time            time.Time
	Cwd             string
	GitBranch       string
	AgentID         string
	IsSidechain     bool
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
	// Group is the session's group membership as a JSON blob (empty when not
	// in a group), opaque here for the same reason as AutoPilot.
	Group string

	Tasks    []TodoItemSnapshot
	Worktree *WorktreeState
}

// TodoItemSnapshot is the persisted shape of a todo.Item. It mirrors that struct
// field for field so the session layer converts by plain struct conversion,
// and carries the same JSON tags so PatchTasks payloads keep their wire form.
type TodoItemSnapshot struct {
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

type Summary struct {
	Provider  string
	Model     string
	ID        string
	FullPath  string
	CreatedAt time.Time
	UpdatedAt time.Time

	Title        string
	LastPrompt   string
	MessageCount int
	GitBranch    string

	IsSidechain bool
}
