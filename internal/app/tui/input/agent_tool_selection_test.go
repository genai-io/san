package input

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/tool/perm"
)

func TestWorkerToolSelectionPreview(t *testing.T) {
	for _, tc := range []struct {
		tools []string
		label string
	}{{nil, "worker defaults"}, {[]string{}, "none"}, {[]string{"Read", "Edit"}, "Read, Edit"}} {
		for _, tool := range []string{"Agent", "SendMessage"} {
			p := NewApproval()
			p.Show(&perm.PermissionRequest{ToolName: tool, Description: "Continue worker: Review", AgentMeta: &perm.AgentMetadata{Tools: tc.tools, PermissionMode: "default"}}, 100, 32)
			view := ansi.Strip(p.Render())
			if !strings.Contains(view, "Tool selection: "+tc.label) || !strings.Contains(view, "Default mode") {
				t.Fatalf("%s selection %#v:\n%s", tool, tc.tools, view)
			}
		}
	}
}
