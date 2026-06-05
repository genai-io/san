// Package hook implements the event hooks system for San.
// Compatible with Claude Code hooks that execute shell commands on events.
package hook

import "context"

// EventType identifies a hook event.
type EventType string

// Application-layer hook events live in the hooks package.
// They intentionally stay separate from core agent lifecycle events.
const (
	SessionStart       EventType = "SessionStart"
	SessionEnd         EventType = "SessionEnd"
	UserPromptSubmit   EventType = "UserPromptSubmit"
	PreToolUse         EventType = "PreToolUse"
	PostToolUse        EventType = "PostToolUse"
	PostToolUseFailure EventType = "PostToolUseFailure"
	PermissionRequest  EventType = "PermissionRequest"
	PermissionDenied   EventType = "PermissionDenied"
	Stop               EventType = "Stop"
	StopFailure        EventType = "StopFailure"
	Notification       EventType = "Notification"
	SubagentStart      EventType = "SubagentStart"
	SubagentStop       EventType = "SubagentStop"
	Setup              EventType = "Setup"
	TaskCreated        EventType = "TaskCreated"
	TaskCompleted      EventType = "TaskCompleted"
	ConfigChange       EventType = "ConfigChange"
	InstructionsLoaded EventType = "InstructionsLoaded"
	CwdChanged         EventType = "CwdChanged"
	FileChanged        EventType = "FileChanged"
	PreCompact         EventType = "PreCompact"
	PostCompact        EventType = "PostCompact"
	WorktreeCreate     EventType = "WorktreeCreate"
	WorktreeRemove     EventType = "WorktreeRemove"
)

// HookInput is the JSON input passed to hook commands via stdin.
type HookInput struct {
	// Common fields
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	PermissionMode string `json:"permission_mode,omitempty"`
	HookEventName  string `json:"hook_event_name"`

	// Tool events
	ToolName     string         `json:"tool_name,omitempty"`
	ToolInput    map[string]any `json:"tool_input,omitempty"`
	ToolUseID    string         `json:"tool_use_id,omitempty"`
	ToolResponse any            `json:"tool_response,omitempty"`
	Error        string         `json:"error,omitempty"`
	IsInterrupt  bool           `json:"is_interrupt,omitempty"`
	Event        string         `json:"event,omitempty"`

	// PermissionRequest suggestions
	PermissionSuggestions []PermissionSuggestion `json:"permission_suggestions,omitempty"`

	// UserPromptSubmit
	Prompt string `json:"prompt,omitempty"`

	// Notification
	Message          string `json:"message,omitempty"`
	Title            string `json:"title,omitempty"`
	NotificationType string `json:"notification_type,omitempty"`

	// Agent events
	AgentID             string `json:"agent_id,omitempty"`
	AgentType           string `json:"agent_type,omitempty"`
	Description         string `json:"description,omitempty"`
	AgentTranscriptPath string `json:"agent_transcript_path,omitempty"`
	StopHookActive      *bool  `json:"stop_hook_active,omitempty"`

	// Stop event
	LastAssistantMessage string `json:"last_assistant_message,omitempty"`

	// Session events
	Source             string `json:"source,omitempty"`
	Model              string `json:"model,omitempty"`
	Reason             string `json:"reason,omitempty"`
	Trigger            string `json:"trigger,omitempty"`
	CustomInstructions string `json:"custom_instructions,omitempty"`

	// Task events
	TaskID          string `json:"task_id,omitempty"`
	TaskSubject     string `json:"task_subject,omitempty"`
	TaskDescription string `json:"task_description,omitempty"`

	// Config/instructions events
	FilePath   string `json:"file_path,omitempty"`
	LoadReason string `json:"load_reason,omitempty"`
	MemoryType string `json:"memory_type,omitempty"`
	OldCwd     string `json:"old_cwd,omitempty"`
	NewCwd     string `json:"new_cwd,omitempty"`

	// Worktree events
	Name         string `json:"name,omitempty"`
	WorktreePath string `json:"worktree_path,omitempty"`
}

// HookOutput is the JSON output from hook commands.
type HookOutput struct {
	Continue           *bool               `json:"continue,omitempty"`
	StopReason         string              `json:"stopReason,omitempty"`
	SuppressOutput     bool                `json:"suppressOutput,omitempty"`
	SystemMessage      string              `json:"systemMessage,omitempty"`
	Decision           string              `json:"decision,omitempty"`
	Reason             string              `json:"reason,omitempty"`
	HookSpecificOutput *HookSpecificOutput `json:"hookSpecificOutput,omitempty"`
}

// HookSpecificOutput contains event-specific output fields.
type HookSpecificOutput struct {
	HookEventName             string                     `json:"hookEventName"`
	PermissionDecision        string                     `json:"permissionDecision,omitempty"`
	PermissionDecisionReason  string                     `json:"permissionDecisionReason,omitempty"`
	UpdatedInput              map[string]any             `json:"updatedInput,omitempty"`
	AdditionalContext         string                     `json:"additionalContext,omitempty"`
	PermissionRequestDecision *PermissionRequestDecision `json:"decision,omitempty"`
	WatchPaths                []string                   `json:"watchPaths,omitempty"`
	InitialUserMessage        string                     `json:"initialUserMessage,omitempty"`
	Retry                     bool                       `json:"retry,omitempty"`
}

// PermissionRequestDecision represents permission request hook decision.
type PermissionRequestDecision struct {
	Behavior           string         `json:"behavior"` // "allow", "deny"
	UpdatedInput       map[string]any `json:"updatedInput,omitempty"`
	UpdatedPermissions []any          `json:"updatedPermissions,omitempty"`
	Message            string         `json:"message,omitempty"`
	Interrupt          bool           `json:"interrupt,omitempty"`
}

// PermissionUpdate represents a structured permission change from a hook response.
// Matches Claude Code's updatedPermissions objects:
//
//	{type: "setMode", mode: "bypassPermissions", destination: "session"}
//	{type: "addRules", rules: [...], behavior: "allow", destination: "session"}
//	{type: "addDirectories", directories: [...], destination: "session"}
type PermissionUpdate struct {
	Type        string           `json:"type"`                  // "setMode", "addRules", "addDirectories"
	Mode        string           `json:"mode,omitempty"`        // for setMode: "bypassPermissions", "acceptEdits", etc.
	Rules       []PermissionRule `json:"rules,omitempty"`       // for addRules
	Behavior    string           `json:"behavior,omitempty"`    // for addRules: "allow", "deny", "ask"
	Directories []string         `json:"directories,omitempty"` // for addDirectories
	Destination string           `json:"destination,omitempty"` // "session" or "persistent"
}

// PermissionRule represents a single permission rule within an addRules update.
type PermissionRule struct {
	ToolName    string `json:"toolName"`
	RuleContent string `json:"ruleContent,omitempty"` // e.g. command prefix for Bash
}

// PermissionSuggestion is a suggested permission change sent in PermissionRequest hook input.
// Matches Claude Code's permission_suggestions field.
type PermissionSuggestion struct {
	Type        string   `json:"type"`                  // "setMode", "addDirectories"
	Mode        string   `json:"mode,omitempty"`        // for setMode
	Directories []string `json:"directories,omitempty"` // for addDirectories
	Destination string   `json:"destination,omitempty"` // "session" or "persistent"
}

// HookOutcome is the processed result from hook execution.
type HookOutcome struct {
	ShouldContinue     bool
	ShouldBlock        bool
	BlockReason        string
	AdditionalContext  string
	UpdatedInput       map[string]any
	PermissionAllow    bool               // Hook explicitly granted permission (allow path)
	ForceAsk           bool               // Hook explicitly requests permission prompt (PreToolUse "ask")
	UpdatedPermissions []PermissionUpdate // Structured permission changes from hook (PermissionRequest only)
	HookSource         string             // Which hook made the decision (for logging)
	WatchPaths         []string
	InitialUserMessage string
	Retry              bool
	Error              error
}

// FunctionHookCallback executes an in-memory hook without spawning a subprocess.
// It returns a structured hook output that is normalized through the same
// response model as command/prompt/agent/http hooks.
type FunctionHookCallback func(ctx context.Context, input HookInput) (HookOutput, error)

// FunctionHook is an in-memory hook registered at runtime or scoped to a
// session. It mirrors Claude Code's session function hooks in spirit: the hook
// is ephemeral, non-persisted, and executes in-process.
type FunctionHook struct {
	ID            string
	Timeout       int
	Once          bool
	StatusMessage string
	Callback      FunctionHookCallback
}

// --- Bidirectional prompt protocol types ---

// PromptRequest is sent by a hook process via stdout to request user input.
// The hook writes one JSON line per request; Claude Code / San reads it,
// collects the answer, and writes a PromptResponse back to the hook's stdin.
type PromptRequest struct {
	Prompt  string         `json:"prompt"`            // request ID / discriminator
	Message string         `json:"message"`           // question text for user
	Options []PromptOption `json:"options,omitempty"` // optional choices
}

// PromptOption is a selectable choice in a PromptRequest.
type PromptOption struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// PromptResponse is sent back to the hook process via stdin.
type PromptResponse struct {
	PromptResponse string `json:"prompt_response"` // matches original Prompt field
	Selected       string `json:"selected"`        // chosen option key or free text
}

// PromptCallback is called by the engine when a hook requests user input.
// Returns the user's response. If cancelled is true, the hook should abort.
type PromptCallback func(req PromptRequest) (resp PromptResponse, cancelled bool)

type AsyncHookResult struct {
	Event       EventType
	HookType    string
	HookSource  string
	HookName    string
	BlockReason string
}

type AsyncHookCallback func(result AsyncHookResult)

// asyncFirstLine is used to detect async hooks via their first stdout line.
type asyncFirstLine struct {
	Async bool `json:"async"`
}
