package transcript

import (
	"time"

	"github.com/genai-io/san/internal/todo"
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

	Messages    []ai.Message
	Notices     []NoticeRecord
	ToolDetails map[string]ToolDetailsRecord
	State       State
}

type State struct {
	Title      string
	LastPrompt string
	Mode       string
	// AutoPilot is the session's autopilot config as a JSON blob (empty when
	// unset). Stored opaque here so the transcript package stays free of an
	// internal/setting dependency; the app marshals/unmarshals it.
	AutoPilot string
	// Group is the session's group membership as a JSON blob (empty when not
	// in a group), opaque here for the same reason as AutoPilot.
	Group string

	Tasks []todo.Item
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
