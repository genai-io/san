package subagent

import (
	"os"
	"path/filepath"
	"testing"
)

func writeAgentFile(t *testing.T, dir, name, frontmatter string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\n" + frontmatter + "\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadAgentsProjectOverridesClaudeCompat(t *testing.T) {
	SetDefaultRegistry(NewRegistry())
	t.Cleanup(ResetDefaultRegistry)

	cwd := t.TempDir()
	writeAgentFile(t, filepath.Join(cwd, ".san", "agents"), "reviewer",
		"name: reviewer\ndescription: san project reviewer")
	writeAgentFile(t, filepath.Join(cwd, ".claude", "agents"), "reviewer",
		"name: reviewer\ndescription: claude compat reviewer")

	LoadAgents(cwd)

	config, ok := defaultRegistry.Get("reviewer")
	if !ok {
		t.Fatal("reviewer not registered")
	}
	if config.Description != "san project reviewer" {
		t.Fatalf("description = %q, want the .san definition to win", config.Description)
	}
}

func TestParseAgentFileAcceptsFrontmatterAliases(t *testing.T) {
	dir := t.TempDir()
	writeAgentFile(t, dir, "test-runner",
		"name: test-runner\ndescription: runs tests\nallowed-tools: [Bash, Read, Grep]\npermission-mode: bypass")

	config, err := parseAgentFile(filepath.Join(dir, "test-runner.md"))
	if err != nil {
		t.Fatalf("parseAgentFile: %v", err)
	}
	if got := config.AllowTools.Names(); len(got) != 3 || got[0] != "Bash" {
		t.Fatalf("allowed-tools alias not applied: %#v", got)
	}
	if config.PermissionMode != PermissionBypass {
		t.Fatalf("permission-mode alias = %q, want bypass", config.PermissionMode)
	}
}

func TestParseAgentFileAcceptsClaudeCodeToolsKey(t *testing.T) {
	dir := t.TempDir()
	writeAgentFile(t, dir, "cc-agent",
		"name: cc-agent\ndescription: cc compat\ntools: Read, Grep")

	config, err := parseAgentFile(filepath.Join(dir, "cc-agent.md"))
	if err != nil {
		t.Fatalf("parseAgentFile: %v", err)
	}
	if got := config.AllowTools.Names(); len(got) != 2 || got[0] != "Read" || got[1] != "Grep" {
		t.Fatalf("tools alias not applied: %#v", got)
	}
}

func TestParseAgentFileCanonicalKeysWinOverAliases(t *testing.T) {
	dir := t.TempDir()
	writeAgentFile(t, dir, "mixed",
		"name: mixed\ndescription: both spellings\nallow_tools: [Read]\ntools: [Bash]\nmode: explore\npermission-mode: bypass")

	config, err := parseAgentFile(filepath.Join(dir, "mixed.md"))
	if err != nil {
		t.Fatalf("parseAgentFile: %v", err)
	}
	if got := config.AllowTools.Names(); len(got) != 1 || got[0] != "Read" {
		t.Fatalf("canonical allow_tools should win: %#v", got)
	}
	if config.PermissionMode != PermissionExplore {
		t.Fatalf("canonical mode should win, got %q", config.PermissionMode)
	}
}
