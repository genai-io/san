package session

import (
	"sync"
	"time"

	"github.com/genai-io/san/v2/internal/session/transcript"
)

var appVersion string
var appVersionOnce sync.Once

// SetAppVersion sets the application version. Only the first call takes effect.
func SetAppVersion(v string) {
	appVersionOnce.Do(func() { appVersion = v })
}

// GetAppVersion returns the application version set by SetAppVersion.
func GetAppVersion() string {
	return appVersion
}

const (
	EntryUser      = "user"
	EntryAssistant = "assistant"
)

type ContentBlock = transcript.ContentBlock
type ImageSource = transcript.ImageSource
type SessionMetadata = transcript.MetadataView

type EntryMessage struct {
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
}

type Entry struct {
	Type        string        `json:"type"`
	ParentUuid  *string       `json:"parentUuid,omitempty"`
	IsSidechain bool          `json:"isSidechain,omitempty"`
	Cwd         string        `json:"cwd,omitempty"`
	SessionID   string        `json:"sessionId,omitempty"`
	Version     string        `json:"version,omitempty"`
	GitBranch   string        `json:"gitBranch,omitempty"`
	AgentID     string        `json:"agentId,omitempty"`
	Message     *EntryMessage `json:"message,omitempty"`
	UUID        string        `json:"uuid,omitempty"`
	Timestamp   time.Time     `json:"timestamp,omitempty"`
}
