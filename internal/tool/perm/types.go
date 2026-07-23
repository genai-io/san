package perm

// PermissionRequest represents a request for user permission before executing a tool
type PermissionRequest struct {
	ID             string         // Unique request ID
	ToolName       string         // Name of the tool requesting permission
	FilePath       string         // File path being modified
	Description    string         // Human-readable description of the action
	CallerAgent    string         // Name of the agent requesting permission (e.g., "@reviewer-1")
	SuggestedRules []string       // Smart allow rule suggestions for "Always allow"
	DiffMeta       *DiffMetadata  // Diff metadata (for Edit/Write tools)
	BashMeta       *BashMetadata  // Bash metadata (for Bash tool)
	SkillMeta      *SkillMetadata // Skill metadata (for Skill tool)
	AgentMeta      *AgentMetadata // Agent metadata (for Agent tool)
}

// DiffMetadata contains diff information for file modifications.
// json tags elide bulky fields (full file contents, unified diff text,
// parsed line array) so the struct is safe to serialize into audit
// records that summarize a diff by counts rather than payload.
type DiffMetadata struct {
	OldContent   string     `json:"-"` // Original file content (bulky; not for audit)
	NewContent   string     `json:"-"` // New file content after modification (bulky)
	UnifiedDiff  string     `json:"-"` // Unified diff format (bulky)
	Lines        []DiffLine `json:"-"` // Parsed diff lines (bulky)
	IsNewFile    bool       `json:"isNewFile,omitempty"`
	PreviewMode  bool       `json:"previewMode,omitempty"`
	AddedCount   int        `json:"addedCount,omitempty"`
	RemovedCount int        `json:"removedCount,omitempty"`
}

// BashMetadata contains metadata for Bash command permission requests.
type BashMetadata struct {
	Command       string `json:"command"`
	Description   string `json:"description,omitempty"`
	RunBackground bool   `json:"runBackground,omitempty"`
	LineCount     int    `json:"lineCount,omitempty"`
}

// SkillMetadata contains metadata for Skill permission requests.
type SkillMetadata struct {
	SkillName   string   `json:"skillName"`
	Description string   `json:"description,omitempty"`
	Args        string   `json:"args,omitempty"`
	ScriptCount int      `json:"scriptCount,omitempty"`
	RefCount    int      `json:"refCount,omitempty"`
	Scripts     []string `json:"scripts,omitempty"`
	References  []string `json:"references,omitempty"`
}

// AgentMetadata contains metadata for Task permission requests.
// Prompt is elided — the same content lives in the surrounding tool_use
// block already, and prompts can be large.
type AgentMetadata struct {
	AgentName      string   `json:"agentName"`
	Description    string   `json:"description,omitempty"`
	Model          string   `json:"model,omitempty"`
	PermissionMode string   `json:"permissionMode,omitempty"`
	Tools          []string `json:"tools,omitempty"`
	Prompt         string   `json:"-"` // bulky; available in the tool_use block
	Background     bool     `json:"background,omitempty"`
}

// DiffLine represents a single line in a diff
type DiffLine struct {
	Type      DiffLineType // Type of diff line
	Content   string       // Line content (without +/- prefix)
	OldLineNo int          // Line number in old file (0 if not applicable)
	NewLineNo int          // Line number in new file (0 if not applicable)
}

// DiffLineType represents the type of a diff line
type DiffLineType int

const (
	DiffLineContext   DiffLineType = iota // Unchanged line (context)
	DiffLineAdded                         // Added line (+)
	DiffLineRemoved                       // Removed line (-)
	DiffLineHunk                          // Hunk header (@@ ... @@)
	DiffLineMetadata                      // Other \-prefixed metadata
	DiffLineNoNewline                     // \ No newline at end of file — format bookkeeping, not content
)
