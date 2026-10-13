package input

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/tool/perm"
)

func TestWorkerApprovalShowsItsContextAndOnlyOneShotChoices(t *testing.T) {
	request := &perm.PermissionRequest{
		ToolName: "Bash", CallerAgent: "worker (task-id)", Description: "Run command",
		WorkingDirectory: "/worker/project", PermissionMode: "Default", OnceOnly: true,
		AllowRules: []string{"Bash(make*)"},
	}
	approval := NewApproval()
	approval.Show(request, 100, 24)
	view := ansi.Strip(approval.Render())
	for _, text := range []string{"worker (task-id)", "/worker/project", "Default"} {
		if !strings.Contains(view, text) {
			t.Fatalf("preview lost %q: %s", text, view)
		}
	}
	if len(BuildApprovalOptions(request)) != 2 {
		t.Fatal("worker has session or persistent grant options")
	}
	if _, response := approval.handleKeypress(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}); response != nil {
		t.Fatal("shift-tab granted a parent-session permission")
	}
	_, response := approval.handleKeypress(tea.KeyPressMsg{Code: '2', Text: "2"})
	if response == nil || response.Approved || response.AllowAll || response.Persist || response.Request != request {
		t.Fatal("second worker option did not reject only that request")
	}
}
