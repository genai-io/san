package perm

import "context"

// Decision represents a permission decision.
type Decision int

const (
	Permit Decision = iota
	Reject
	Prompt
)

// String renders the decision for logs and audit records.
func (d Decision) String() string {
	switch d {
	case Permit:
		return "permit"
	case Reject:
		return "reject"
	case Prompt:
		return "prompt"
	default:
		return "unknown"
	}
}

// IsEditTool reports whether the tool mutates files (vs. shell exec or
// other side effects). Used to gate `acceptEdits`-mode auto-approval.
func IsEditTool(name string) bool {
	switch name {
	case "Edit", "Write", "NotebookEdit":
		return true
	}
	return false
}

// --- Tool classification ---

var readOnlyTools = map[string]bool{
	"Read":      true,
	"Glob":      true,
	"Grep":      true,
	"WebFetch":  true,
	"WebSearch": true,
	"LSP":       true,
}

// IsReadOnlyTool reports whether the tool only reads from the workspace
// or remote sources. Used by callers that need a stricter classification
// than IsSafeTool (which also covers task / question tools).
func IsReadOnlyTool(name string) bool {
	return readOnlyTools[name]
}

var safeTools = func() map[string]bool {
	m := map[string]bool{
		"TaskCreate":      true,
		"TaskGet":         true,
		"TaskList":        true,
		"TaskUpdate":      true,
		"AskUserQuestion": true,
		"CronList":        true,
		// Evolve only queues a background self-learning review — it writes
		// nothing itself, so it auto-allows like the task/question tools.
		"Evolve": true,
	}
	for name := range readOnlyTools {
		m[name] = true
	}
	return m
}()

// IsSafeTool reports whether the tool is on the safe allowlist.
// Safe tools auto-allow under every mode (subject to deny rules and
// bypass-immune checks).
func IsSafeTool(name string) bool {
	return safeTools[name]
}

// PermissionFunc gates tool execution. Called with the tool name and
// parsed input. May block (e.g., to wait for TUI approval).
type PermissionFunc func(ctx context.Context, name string, input map[string]any) (allow bool, reason string)
