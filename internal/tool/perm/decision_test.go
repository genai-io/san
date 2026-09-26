package perm

import "testing"

func TestIsSafeTool(t *testing.T) {
	safe := []string{"TaskCreate", "TaskGet", "TaskUpdate",
		"AskUserQuestion", "LSP", "Evolve"}
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
