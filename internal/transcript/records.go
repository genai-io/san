package transcript

import (
	"encoding/json"
	"time"

	"github.com/genai-io/san/internal/todo"
	"github.com/genai-io/san/internal/tool/toolresult"
	"github.com/genai-io/sdk-go/pkg/ai"
)

// Record type values follow <entity>.<verb> (past tense), lowercase,
// dot-separated. See docs/inspector.md for the full taxonomy.
const (
	SchemaVersion = "4"

	SessionStarted       = "session.started"
	SessionForked        = "session.forked"
	SessionCompacted     = "session.compacted"
	SessionStatePatched  = "session.state.patched"
	MessageAppended      = "message.appended"
	ConversationReplaced = "conversation.replaced"
	InferenceRequested   = "inference.requested"
	InferenceResponded   = "inference.responded"
	InferenceFailed      = "inference.failed"
	SystemSectionAdded   = "system.section.added"
	SystemSectionRemoved = "system.section.removed"
	ToolAdded            = "tool.added"
	ToolRemoved          = "tool.removed"
	HookFired            = "hook.fired"
	PermissionRequired   = "permission.required"
	PermissionDecided    = "permission.decided"
	SkillStateChanged    = "skill.state.changed"
	NoticeAdded          = "notice.added"
	ToolCompleted        = "tool.completed"
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

// Record is one transcript event. On disk a message is the session log's own
// message record and every other Record is written whole as the data of an
// application record, so Message and Messages are never part of that.
type Record struct {
	ID        string    `json:"-"` // its position in the log, assigned on read
	SessionID string    `json:"sessionId"`
	Time      time.Time `json:"time"`
	Type      string    `json:"type"`

	Cwd       string `json:"cwd,omitempty"`
	Version   string `json:"version,omitempty"`
	GitBranch string `json:"gitBranch,omitempty"`

	Messages    []ai.Message         `json:"-"`
	Message     *ai.Message          `json:"-"`
	State       *StatePatch          `json:"state,omitempty"`
	Start       *SessionStart        `json:"start,omitempty"`
	Fork        *SessionFork         `json:"fork,omitempty"`
	Compaction  *Compaction          `json:"compaction,omitempty"`
	Inference   *InferenceRecord     `json:"inference,omitempty"`
	System      *SystemSectionRecord `json:"system,omitempty"`
	Tool        *ToolRecord          `json:"tool,omitempty"`
	Hook        *HookRecord          `json:"hook,omitempty"`
	Permission  *PermissionRecord    `json:"permission,omitempty"`
	Skill       *SkillRecord         `json:"skill,omitempty"`
	ToolDetails *ToolDetailsRecord   `json:"toolDetails,omitempty"`
	Notice      *NoticeRecord        `json:"notice,omitempty"`
}

// StatePatch updates selected fields; nil leaves a field unchanged.
// A non-nil pointer to an empty value explicitly clears it.
type StatePatch struct {
	Title      *string      `json:"title,omitempty"`
	LastPrompt *string      `json:"lastPrompt,omitempty"`
	Mode       *string      `json:"mode,omitempty"`
	AutoPilot  *string      `json:"autoPilot,omitempty"`
	Group      *string      `json:"group,omitempty"`
	Tasks      *[]todo.Item `json:"tasks,omitempty"`
}

// SessionStart describes a new identity and its initial runtime configuration.
type SessionStart struct {
	Provider        string `json:"provider,omitempty"`
	Model           string `json:"model,omitempty"`
	MaxTokens       int    `json:"maxTokens,omitempty"`
	AgentID         string `json:"agentId,omitempty"`
	ParentSessionID string `json:"parentSessionId,omitempty"`
	Sidechain       bool   `json:"sidechain,omitempty"`
}

type SessionFork struct {
	ParentSessionID string `json:"parentSessionId"`
}
type Compaction struct {
	MessageID string `json:"messageId"`
}

// InferenceRecord carries the payload for inference.requested /
// inference.responded. The "requested" side captures the digests of what was
// sent to the LLM (system prompt, tools, active message chain); the "responded"
// side captures stop reason, latency, and token usage. Big fields live in the
// digests — full payloads are reconstructible by replaying preceding records.
//
// Sequence joins one inference request with its outcome. Exchange identifies
// the user exchange; Attempt rises on retries. Model settings belong to the call.
type InferenceRecord struct {
	Sequence     int    `json:"sequence"`
	Exchange     int    `json:"exchange,omitempty"`
	Provider     string `json:"provider,omitempty"`
	Model        string `json:"model,omitempty"`
	MaxTokens    int    `json:"maxTokens,omitempty"`
	SystemDigest string `json:"systemDigest,omitempty"`
	ToolsDigest  string `json:"toolsDigest,omitempty"`

	// MessageIDs is the active chain at request time, in send order.
	// Recorded on inference.requested only.
	MessageIDs []string `json:"messageIds,omitempty"`

	// Response fields — populated on inference.responded only.
	StopReason string          `json:"stopReason,omitempty"`
	LatencyMs  int64           `json:"latencyMs,omitempty"`
	Usage      *InferenceUsage `json:"usage,omitempty"`

	// Failure fields — populated on inference.failed only. Attempt is the
	// loop's own count of calls in this step, rising across recoveries as
	// well as retries. Retryable is how the error was classified, not
	// whether a retry followed, which is what makes a misclassification
	// visible after the fact.
	Error     string `json:"error,omitempty"`
	Attempt   int    `json:"attempt,omitempty"`
	Retryable bool   `json:"retryable,omitempty"`
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

// ToolRecord carries the payload for tool.added / tool.removed. One tool
// per record (Add/Remove fire individually); Schema is set on "added",
// Name on "removed".
type ToolRecord struct {
	Schema *ai.Schema `json:"schema,omitempty"`
	Name   string     `json:"name,omitempty"`
	Caller string     `json:"caller,omitempty"`
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

// NoticeRecord is a line shown to the person and never to the model — a
// command's output, a group change — kept so a resume shows it where it was:
// after AfterMessageID, or before every message when that is empty.
type NoticeRecord struct {
	AfterMessageID string `json:"afterMessageId,omitempty"`
	Text           string `json:"text"`
	Agent          bool   `json:"agent,omitempty"` // drawn as an agent's notice
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

// ToolDetailsRecord keeps presentation data beside the unmodified model result.
type ToolDetailsRecord struct {
	ToolCallID string                        `json:"toolCallId"`
	FileChange *toolresult.FileChangeDetails `json:"fileChange,omitempty"`
	Bash       *toolresult.BashDetails       `json:"bash,omitempty"`
	Agent      *toolresult.AgentDetails      `json:"agent,omitempty"`
}
