package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/setting"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestProjectModelRoutingRequiresTrustReview(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SAN_TRUST_WORKSPACE", "")
	cwd := t.TempDir()
	dir := filepath.Join(cwd, ".san")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "providers.json"), []byte(`{"current":{"modelId":"project-model","provider":"openai","authMethod":"api_key"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := pendingTrustFindings(cwd)
	if len(findings) != 1 || !strings.Contains(findings[0], "project-model") || !strings.Contains(findings[0], "providers.json") {
		t.Fatalf("model routing missing from trust review: %v", findings)
	}
	if setting.IsWorkspaceTrusted(cwd) {
		t.Fatal("project model routing was silently trusted")
	}
}

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
