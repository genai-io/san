package transcript

import (
	"encoding/json"
	"time"
)

// Record type values follow <entity>.<verb> (past tense), lowercase,
// dot-separated. See docs/tracing.md for the full taxonomy.
const (
	SessionStarted       = "session.started"
	SessionForked        = "session.forked"
	SessionCompacted     = "session.compacted"
	MessageAppended      = "message.appended"
	StatePatched         = "state.patched"
	InferenceRequested   = "inference.requested"
	InferenceResponded   = "inference.responded"
	SystemSectionAdded   = "system.section.added"
	SystemSectionRemoved = "system.section.removed"
	ToolsAdded           = "tools.added"
	ToolsRemoved         = "tools.removed"
)

const (
	PatchPathTitle      = "title"
	PatchPathLastPrompt = "lastPrompt"
	PatchPathTag        = "tag"
	PatchPathMode       = "mode"
	PatchPathTasks      = "tasks"
	PatchPathWorktree   = "worktree"
)

type Record struct {
	ID        string    `json:"id"`
	SessionID string    `json:"sessionId"`
	Time      time.Time `json:"time"`
	Type      string    `json:"type"`

	ParentID    string `json:"parentId,omitempty"`
	IsSidechain bool   `json:"isSidechain,omitempty"`
	Cwd         string `json:"cwd,omitempty"`
	Version     string `json:"version,omitempty"`
	GitBranch   string `json:"gitBranch,omitempty"`
	AgentID     string `json:"agentId,omitempty"`

	Message   *MessageRecord       `json:"message,omitempty"`
	State     *StateRecord         `json:"state,omitempty"`
	Session   *SessionRecord       `json:"session,omitempty"`
	Inference *InferenceRecord     `json:"inference,omitempty"`
	System    *SystemSectionRecord `json:"system,omitempty"`
	Tools     *ToolsRecord         `json:"tools,omitempty"`
}

type MessageRecord struct {
	MessageID string         `json:"messageId"`
	Role      string         `json:"role"`
	Content   []ContentBlock `json:"content"`
}

type StateRecord struct {
	Ops []PatchOp `json:"ops"`
}

type PatchOp struct {
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value"`
}

// SessionRecord carries the lifecycle payload for session.started /
// session.forked / session.compacted records. The three event types
// multiplex on a single struct because the fields are sparse and the
// projector dispatches on Record.Type rather than payload shape.
type SessionRecord struct {
	Provider   string `json:"provider,omitempty"`
	Model      string `json:"model,omitempty"`
	ParentID   string `json:"parentId,omitempty"`
	BoundaryID string `json:"boundaryId,omitempty"`
}

// InferenceRecord carries the payload for inference.requested /
// inference.responded. The "requested" side captures the digests of what was
// sent to the LLM (system prompt, tools, active message chain); the "responded"
// side captures stop reason, latency, and token usage. Big fields live in the
// digests — full payloads are reconstructible by replaying preceding records.
type InferenceRecord struct {
	Turn         int    `json:"turn"`
	Provider     string `json:"provider,omitempty"`
	Model        string `json:"model,omitempty"`
	MaxTokens    int    `json:"maxTokens,omitempty"`
	SystemDigest string `json:"systemDigest,omitempty"`
	ToolsDigest  string `json:"toolsDigest,omitempty"`

	// MessageIDs is the active chain at request time, in send order.
	// Recorded on inference.requested only.
	MessageIDs []string `json:"messageIds,omitempty"`

	// Response fields — populated on inference.responded only.
	StopReason string         `json:"stopReason,omitempty"`
	LatencyMs  int64          `json:"latencyMs,omitempty"`
	Usage      *InferenceUsage `json:"usage,omitempty"`
}

type InferenceUsage struct {
	InputTokens       int `json:"inputTokens"`
	OutputTokens      int `json:"outputTokens"`
	CacheCreateTokens int `json:"cacheCreateTokens,omitempty"`
	CacheReadTokens   int `json:"cacheReadTokens,omitempty"`
}

// SystemSectionRecord carries the payload for system.section.added /
// system.section.removed. On removal, Content is empty.
type SystemSectionRecord struct {
	Name    string `json:"name"`
	Slot    int    `json:"slot,omitempty"`
	Content string `json:"content,omitempty"`
	Caller  string `json:"caller,omitempty"`
}

// ToolSchemaView mirrors a tool schema in transcript-local types so the
// transcript package stays free of cross-package imports. Recorder converts
// from the runtime ToolSchema before writing.
type ToolSchemaView struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// ToolsRecord carries the payload for tools.added / tools.removed.
// One tool per record (Add/Remove fire individually); Schema is set on
// "added", Name on "removed".
type ToolsRecord struct {
	Schema *ToolSchemaView `json:"schema,omitempty"`
	Name   string          `json:"name,omitempty"`
	Caller string          `json:"caller,omitempty"`
}

type ContentBlock struct {
	Type string `json:"type"`

	Text string `json:"text,omitempty"`

	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`

	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   []ContentBlock `json:"content,omitempty"`
	IsError   bool           `json:"is_error,omitempty"`

	// Source marks the provenance of injected content (e.g. "hook:UserPromptSubmit",
	// "command:/identity", "reminder:system-reminder"). Empty for user-authored
	// blocks and for ContentBlocks that the model itself produced.
	Source string `json:"source,omitempty"`

	// ImageSource is the inlined image data on type=image blocks.
	ImageSource *ImageSource `json:"imageSource,omitempty"`
}

type ImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type WorktreeState struct {
	OriginalCwd    string `json:"originalCwd"`
	WorktreePath   string `json:"worktreePath"`
	WorktreeName   string `json:"worktreeName"`
	WorktreeBranch string `json:"worktreeBranch,omitempty"`
	OriginalBranch string `json:"originalBranch,omitempty"`
	Exited         bool   `json:"exited,omitempty"`
}
