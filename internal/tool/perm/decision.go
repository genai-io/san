package perm

import (
	"context"
	"fmt"
)

// Decision represents a permission decision.
type Decision int

const (
	Permit Decision = iota
	Reject
	Prompt
)

// Checker decides whether a tool call is permitted.
// Implementations correspond 1:1 to the modes in docs/concepts/permission-model.md
// and are used as step 7 ("mode default") of the unified pipeline.
type Checker interface {
	Check(name string, params map[string]any) Decision
}

// Default is the policy for `default` mode: safe tools auto-allowed,
// everything else prompts the user.
func Default() Checker { return defaultChecker{} }

// AcceptEdits is the policy for `acceptEdits` (and currently `auto`) mode:
// safe + edit tools auto-allowed, everything else prompts.
func AcceptEdits() Checker { return acceptEditsChecker{} }

// ReadOnly is the policy for `explore` mode: safe tools auto-allowed,
// everything else explicitly rejected (no prompt — used in subagents
// where "read-only intent" must be visible to the model).
func ReadOnly() Checker { return readOnlyChecker{} }

// PermitAll is the policy for `bypassPermissions` mode: everything
// auto-allowed. Bypass-immune checks (sensitive paths, destructive
// commands) are enforced upstream and are not affected by this checker.
func PermitAll() Checker { return permitAllChecker{} }

// DenyAll is the policy that rejects every tool call. Used as a fallback
// when no checker is configured.
func DenyAll() Checker { return denyAllChecker{} }

type denyAllChecker struct{}

func (denyAllChecker) Check(_ string, _ map[string]any) Decision { return Reject }

type defaultChecker struct{}

func (defaultChecker) Check(name string, _ map[string]any) Decision {
	if IsSafeTool(name) {
		return Permit
	}
	return Prompt
}

type acceptEditsChecker struct{}

func (acceptEditsChecker) Check(name string, _ map[string]any) Decision {
	if IsSafeTool(name) || IsEditTool(name) {
		return Permit
	}
	return Prompt
}

type readOnlyChecker struct{}

func (readOnlyChecker) Check(name string, _ map[string]any) Decision {
	if IsSafeTool(name) {
		return Permit
	}
	return Reject
}

type permitAllChecker struct{}

func (permitAllChecker) Check(_ string, _ map[string]any) Decision { return Permit }

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

// AsPermissionFunc converts a Checker into a PermissionFunc.
// `Reject` becomes (false, reason); `Permit` and `Prompt` both become
// (true, ""). Use this only when the caller will not handle Prompt
// downstream — otherwise compose the Checker into a custom function.
func AsPermissionFunc(c Checker) PermissionFunc {
	if c == nil {
		return nil
	}
	return func(_ context.Context, name string, input map[string]any) (bool, string) {
		if c.Check(name, input) == Reject {
			return false, fmt.Sprintf("tool %s is not permitted in this mode", name)
		}
		return true, ""
	}
}
