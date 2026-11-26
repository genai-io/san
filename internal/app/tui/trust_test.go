package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/setting/trust"

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
	findings := trust.ReviewAndTrustIfClean(cwd)
	if len(findings) != 1 || !strings.Contains(findings[0], "project-model") || !strings.Contains(findings[0], "providers.json") {
		t.Fatalf("model routing missing from trust review: %v", findings)
	}
	if setting.IsWorkspaceTrusted(cwd) {
		t.Fatal("project model routing was silently trusted")
	}
}

func TestTrackedDotenvAloneRequiresTrustReview(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("SAN_TRUST_WORKSPACE", "")
	cwd := t.TempDir()
	if out, err := exec.Command("git", "-C", cwd, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".env"), []byte("SAN_TEST_DOTENV=local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", cwd, "add", "-f", ".env").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	findings := trust.ReviewAndTrustIfClean(cwd)
	if len(findings) != 1 || !strings.Contains(findings[0], ".env") || setting.IsWorkspaceTrusted(cwd) {
		t.Fatalf("tracked .env should require trust review, got %v", findings)
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
