package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Long paths in the trust prompt wrap to the terminal instead of running off it.
func TestTrustPromptWrapsLongPaths(t *testing.T) {
	long := "/Users/someone/" + strings.Repeat("deeply-nested-folder/", 8) + "repo"
	m, _ := trustPrompt{folder: long, findings: []string{"start MCP server x: node " + long + "/server.js  (.san/mcp.json)"}}.
		Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	view := m.(trustPrompt).View().Content
	for line := range strings.SplitSeq(view, "\n") {
		if w := ansi.StringWidth(line); w > 60 {
			t.Errorf("line is %d wide, want ≤ 60: %q", w, line)
		}
	}
	if !strings.Contains(ansi.Strip(view), "repo") {
		t.Error("path tail lost")
	}
}
