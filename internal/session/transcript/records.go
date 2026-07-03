package transcript

import (
	"encoding/json"
	"time"
)

// Record type values follow <entity>.<verb> (past tense), lowercase,
// dot-separated. See docs/inspector.md for the full taxonomy.
const (
	SchemaVersion = "1"

	SessionStarted       = "session.started"
	SessionForked        = "session.forked"
	SessionCompacted     = "session.compacted"
	SessionStatePatched  = "session.state.patched"
	MessageAppended      = "message.appended"
	InferenceRequested   = "inference.requested"
	InferenceResponded   = "inference.responded"
	SystemSectionAdded   = "system.section.added"
	SystemSectionRemoved = "system.section.removed"
	ToolAdded            = "tool.added"
	ToolRemoved          = "tool.removed"
	HookFired            = "hook.fired"
	PermissionRequired   = "permission.required"
	PermissionDecided    = "permission.decided"
	SkillStateChanged    = "skill.state.changed"
)

const (
	PatchPathTitle      = "title"
	PatchPathLastPrompt = "lastPrompt"
	PatchPathTag        = "tag"
	PatchPathMode       = "mode"
	PatchPathTasks      = "tasks"
	PatchPathWorktree   = "worktree"
)

// Audit-record enum values. Centralized so producers and consumers (viewer,
// audit tooling) can't drift on spelling.
const (
	// HookRecord.Outcome
	HookOutcomeRan     = "ran"
	HookOutcomeBlocked = "blocked"
	HookOutcomeError   = "error"
	HookOutcomeAsync   = "async"

	// PermissionRecord.Decision
	PermissionPermit = "permit"
	PermissionReject = "reject"

	// PermissionRecord.Source
	PermissionSourceConfig   = "config"
	PermissionSourceUser     = "user"
	PermissionSourceHook     = "hook"
	PermissionSourceAsk      = "ask"
	PermissionSourceReviewer = "reviewer"
)

// PermissionRecord.Scope is a free-form label owned by the source of the
// decision (e.g. the TUI approval modal). The transcript schema deliberately
// does not enumerate values here so adding a new modal option (or a
// programmatic decision source) doesn't require a schema change.

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

	Message    *MessageRecord       `json:"message,omitempty"`
	State      *StateRecord         `json:"state,omitempty"`
	Session    *SessionRecord       `json:"session,omitempty"`
	Inference  *InferenceRecord     `json:"inference,omitempty"`
	System     *SystemSectionRecord `json:"system,omitempty"`
	Tool       *ToolRecord          `json:"tool,omitempty"`
	Hook       *HookRecord          `json:"hook,omitempty"`
	Permission *PermissionRecord    `json:"permission,omitempty"`
	Skill      *SkillRecord         `json:"skill,omitempty"`
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
//
// session.started carries every session-wide constant exactly once
// (provider, model, maxTokens, agentId). Every following record inherits
// them — they are not restamped per record.
type SessionRecord struct {
	Provider  string `json:"provider,omitempty"`
	Model     string `json:"model,omitempty"`
	MaxTokens int    `json:"maxTokens,omitempty"`
	AgentID   string `json:"agentId,omitempty"`
	ParentID  string `json:"parentId,omitempty"`
	// SummaryMessageID, on a session.compacted record, is the ID of the summary
	// message that replaced the pre-compaction chain. Replay uses it as the
	// boundary to stop walking parents at.
	SummaryMessageID string `json:"summaryMessageId,omitempty"`
}

// InferenceRecord carries the payload for inference.requested /
// inference.responded. The "requested" side captures the digests of what was
// sent to the LLM (system prompt, tools, active message chain); the "responded"
// side captures stop reason, latency, and token usage. Big fields live in the
// digests — full payloads are reconstructible by replaying preceding records.
//
// Provider/Model/MaxTokens are NOT on this record. They are session-wide
// constants set on session.started and inherited by every turn. Request
// and response are joined by Turn.
type InferenceRecord struct {
	Turn         int    `json:"turn"`
	SystemDigest string `json:"systemDigest,omitempty"`
	ToolsDigest  string `json:"toolsDigest,omitempty"`

	// MessageIDs is the active chain at request time, in send order.
	// Recorded on inference.requested only.
	MessageIDs []string `json:"messageIds,omitempty"`

	// Response fields — populated on inference.responded only.
	StopReason string          `json:"stopReason,omitempty"`
	LatencyMs  int64           `json:"latencyMs,omitempty"`
	Usage      *InferenceUsage `json:"usage,omitempty"`
}

type InferenceUsage struct {
	InputTokens              int `json:"inputTokens"`
	OutputTokens             int `json:"outputTokens"`
	CacheCreationInputTokens int `json:"cacheCreateTokens,omitempty"`
	CacheReadInputTokens     int `json:"cacheReadTokens,omitempty"`
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
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"input_schema,omitempty"`
}

func (t *ToolSchemaView) UnmarshalJSON(data []byte) error {
	type alias ToolSchemaView
	var v struct {
		alias
		LegacyParameters json.RawMessage `json:"parameters,omitempty"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*t = ToolSchemaView(v.alias)
	if len(t.Parameters) == 0 && len(v.LegacyParameters) > 0 {
		t.Parameters = v.LegacyParameters
	}
	return nil
}

// ToolRecord carries the payload for tool.added / tool.removed. One tool
// per record (Add/Remove fire individually); Schema is set on "added",
// Name on "removed".
type ToolRecord struct {
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

// HookRecord carries the payload for hook.fired. One record per
// completed hook invocation (sync or async). Outcome captures what the
// hook actually did so the transcript reads as "did this hook block /
// inject context / approve / fail" without re-parsing hook output.
type HookRecord struct {
	Event     string `json:"event"`             // e.g. "PreToolUse", "PostCompact"
	Source    string `json:"source,omitempty"`  // hook script path / function ID
	Matcher   string `json:"matcher,omitempty"` // hook config matcher
	Outcome   string `json:"outcome"`           // "ran" | "blocked" | "error" | "async"
	Reason    string `json:"reason,omitempty"`  // hook-supplied block/deny message
	LatencyMs int64  `json:"latencyMs,omitempty"`
}

// PermissionRecord carries the payload for permission.required and
// permission.decided. The two share a payload because their fields overlap
// heavily and joining them by RequestID at replay time is trivial.
//
// permission.required is emitted when the config-level check can't decide
// alone and an external resolver (user prompt, hook) must adjudicate.
// permission.decided is emitted when a terminal allow/deny is reached
// (immediately, for config-level allow/deny; later, when the external
// resolver responds for ask-level decisions).
//
// Input carries the tool arguments being adjudicated (Bash command, file
// path, skill name, etc.) so the transcript is self-sufficient — auditors
// don't have to cross-reference the surrounding tool_use block to see what
// was being asked. Recorded as json.RawMessage to preserve the model-facing
// shape verbatim and tolerate arbitrary tool schemas.
//
// Decision and Source are denormalized strings rather than enum constants
// so the transcript stays decoupled from the perm package's internal types.
type PermissionRecord struct {
	RequestID      string          `json:"requestId,omitempty"`      // joins required → decided
	Tool           string          `json:"tool"`                     // tool name
	Input          json.RawMessage `json:"input,omitempty"`          // tool args being adjudicated (model intent)
	Detail         json.RawMessage `json:"detail,omitempty"`         // resolved context the user saw (skill description, bash line count, diff stats, ...)
	OptionsOffered []string        `json:"optionsOffered,omitempty"` // labels of the choices presented (required only)
	Decision       string          `json:"decision,omitempty"`       // "permit" | "reject" on decided; absent on required
	Source         string          `json:"source,omitempty"`         // "config" | "user" | "hook" | "mode"
	Scope          string          `json:"scope,omitempty"`          // free-form label owned by the source (e.g. modal: "once"/"session"/"persistent")
	Reason         string          `json:"reason,omitempty"`
	Mode           string          `json:"mode,omitempty"`      // active permission mode at decision time
	LatencyMs      int64           `json:"latencyMs,omitempty"` // on decided only: time since required
}

// SkillRecord carries the payload for skill.state.changed. Emitted whenever
// a skill transitions between disable / enable / active so the transcript
// captures "what was the model aware of" at any point. Active skills end up
// in the reminder pipeline; enable-state skills are user-invokable only;
// disable hides them entirely.
type SkillRecord struct {
	Name     string `json:"name"`
	Previous string `json:"previous,omitempty"` // disable | enable | active | "" (first observation)
	Current  string `json:"current"`            // disable | enable | active
	Caller   string `json:"caller,omitempty"`   // "user:/skills" | "boot" | etc.
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
