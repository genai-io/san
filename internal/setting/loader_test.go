package setting

import (
	"testing"

	"github.com/genai-io/san/internal/proc"
)

func TestWithDefaultDisabledToolsOverlay(t *testing.T) {
	// Absent key falls back to the factory default.
	got := WithDefaultDisabledTools(nil)
	if !got["Cron"] {
		t.Fatal("Cron should be disabled by default")
	}

	// An explicit false entry (the /tool panel's enable) overrides the default.
	got = WithDefaultDisabledTools(map[string]bool{"Cron": false})
	if got["Cron"] {
		t.Fatal("explicit enable must override the factory default")
	}

	// Explicit disables of normal tools pass through.
	got = WithDefaultDisabledTools(map[string]bool{"WebSearch": true})
	if !got["WebSearch"] || !got["Cron"] {
		t.Fatalf("unexpected map: %#v", got)
	}

	// The task tracker tools ship disabled by default, and re-enabling one from
	// the /tool panel (an explicit false) overrides that.
	got = WithDefaultDisabledTools(nil)
	for _, name := range []string{"TaskCreate", "TaskGet", "TaskUpdate"} {
		if !got[name] {
			t.Fatalf("%s should be disabled by default", name)
		}
	}
	got = WithDefaultDisabledTools(map[string]bool{"TaskCreate": false})
	if got["TaskCreate"] {
		t.Fatal("explicit enable must override the task-tracker default")
	}

	// Inter-agent controls and Workflow ship disabled by default.
	for _, name := range []string{"SendMessage", "AgentStop", "Workflow"} {
		if !WithDefaultDisabledTools(nil)[name] {
			t.Fatalf("%s should be disabled by default", name)
		}
	}
}

// On a Windows with both Git Bash and PowerShell the second shell's tool ships
// disabled — the /tools panel turns it on — and the default one does not.
func TestSecondShellShipsDisabled(t *testing.T) {
	shells := proc.Shells()
	if len(shells) < 2 {
		t.Skip("this machine offers one shell")
	}
	def, second := shells[0].Kind.ToolName(), shells[1].Kind.ToolName()
	disabled := WithDefaultDisabledTools(nil)
	if !IsDefaultDisabledTool(second) || !disabled[second] {
		t.Errorf("%s, the second shell, does not ship disabled", second)
	}
	if IsDefaultDisabledTool(def) || disabled[def] {
		t.Errorf("%s, the default shell, ships disabled", def)
	}
	if WithDefaultDisabledTools(map[string]bool{second: false})[second] {
		t.Errorf("turning %s on in /tools did not override its default", second)
	}
}
