package perm

import "testing"

func TestIsReadOnlyTool(t *testing.T) {
	readOnly := []string{"Read", "Glob", "Grep", "WebFetch", "WebSearch", "LSP"}
	for _, name := range readOnly {
		if !IsReadOnlyTool(name) {
			t.Errorf("IsReadOnlyTool(%q) = false, want true", name)
		}
	}

	notReadOnly := []string{"Bash", "Write", "Edit", "Agent"}
	for _, name := range notReadOnly {
		if IsReadOnlyTool(name) {
			t.Errorf("IsReadOnlyTool(%q) = true, want false", name)
		}
	}
}

func TestIsSafeTool(t *testing.T) {
	safe := []string{"TaskCreate", "TaskGet", "TaskList", "TaskUpdate",
		"AskUserQuestion", "LSP"}
	for _, name := range safe {
		if !IsSafeTool(name) {
			t.Errorf("IsSafeTool(%q) = false, want true", name)
		}
	}

	notSafe := []string{"Edit", "Bash", "Write", "Agent"}
	for _, name := range notSafe {
		if IsSafeTool(name) {
			t.Errorf("IsSafeTool(%q) = true, want false", name)
		}
	}
}

// Mode-default policy (formerly the perm.Checker family) now lives in
// setting.ModeDefault; its behavior is covered by setting and subagent tests.
