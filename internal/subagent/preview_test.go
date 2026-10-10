package subagent

import (
	"slices"
	"testing"

	"github.com/genai-io/san/internal/tool"
)

func TestBuiltinToolNamesMatchRuntimeModeFiltering(t *testing.T) {
	explore := BuiltinToolNames(PermissionExplore)
	edit := BuiltinToolNames(PermissionAcceptEdits)

	for _, name := range []string{tool.ToolRead, tool.ToolSkill, tool.ToolSendMessage} {
		if !slices.Contains(explore, name) || !slices.Contains(edit, name) {
			t.Errorf("%s missing from explore or edit preview", name)
		}
	}
	for _, name := range []string{tool.ToolEdit, tool.ToolWrite} {
		if slices.Contains(explore, name) || !slices.Contains(edit, name) {
			t.Errorf("%s has wrong visibility across explore/edit previews", name)
		}
	}
	for _, name := range []string{tool.ToolAgent, tool.ToolAgentStop, tool.ToolWorkflow} {
		if slices.Contains(explore, name) || slices.Contains(edit, name) {
			t.Errorf("parent-only tool %s appears in a preview", name)
		}
	}
}
