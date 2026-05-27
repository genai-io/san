package input

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleMemoryEditProjectUsesInstructionPrecedence(t *testing.T) {
	cwd := t.TempDir()
	writeMemoryTestFile(t, filepath.Join(cwd, "AGENTS.md"), "codex")

	path, err := handleMemoryEdit(cwd, "project")
	if err != nil || path != filepath.Join(cwd, "AGENTS.md") {
		t.Fatalf("AGENTS fallback = %q, %v", path, err)
	}

	writeMemoryTestFile(t, filepath.Join(cwd, "CLAUDE.md"), "claude")
	path, err = handleMemoryEdit(cwd, "project")
	if err != nil || path != filepath.Join(cwd, "CLAUDE.md") {
		t.Fatalf("CLAUDE precedence = %q, %v", path, err)
	}

	writeMemoryTestFile(t, filepath.Join(cwd, ".gen", "GEN.md"), "gen")
	path, err = handleMemoryEdit(cwd, "project")
	if err != nil || path != filepath.Join(cwd, ".gen", "GEN.md") {
		t.Fatalf("GEN precedence = %q, %v", path, err)
	}
}

func TestMemoryUIUsesLoadedFallbackWhenHigherPrecedenceFileIsEmpty(t *testing.T) {
	cwd := t.TempDir()
	writeMemoryTestFile(t, filepath.Join(cwd, "CLAUDE.md"), "\n")
	writeMemoryTestFile(t, filepath.Join(cwd, "AGENTS.md"), "codex active instructions")

	path, err := handleMemoryEdit(cwd, "project")
	if err != nil || path != filepath.Join(cwd, "AGENTS.md") {
		t.Fatalf("project edit with empty CLAUDE fallback = %q, %v", path, err)
	}

	selector := NewMemorySelector()
	selector.EnterSelect(cwd, 120, 40)
	project := selector.items[1]
	if project.Path != filepath.Join(cwd, "AGENTS.md") {
		t.Fatalf("selector project path = %q, want active AGENTS.md", project.Path)
	}

	list, err := handleMemoryList(cwd)
	if err != nil || !strings.Contains(list, "AGENTS.md") || strings.Contains(list, "CLAUDE.md") {
		t.Fatalf("/memory list should show only active fallback, got %q, %v", list, err)
	}
}

func TestMemoryEditCanOpenEmptyDraftWithoutActiveFallback(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "CLAUDE.md")
	writeMemoryTestFile(t, path, "\n")

	got, err := handleMemoryEdit(cwd, "project")
	if err != nil || got != path {
		t.Fatalf("empty draft edit = %q, %v; want %q", got, err, path)
	}
}

func TestHandleInitCommandCreatesCodexAndClaudeTemplates(t *testing.T) {
	codexDir := t.TempDir()
	result, err := HandleInitCommand(codexDir, "--codex")
	if err != nil || !strings.Contains(result, "AGENTS.md") {
		t.Fatalf("/init --codex = %q, %v", result, err)
	}
	body, err := os.ReadFile(filepath.Join(codexDir, "AGENTS.md"))
	if err != nil || !strings.HasPrefix(string(body), "# AGENTS.md") {
		t.Fatalf("AGENTS.md body = %q, %v", body, err)
	}

	claudeDir := t.TempDir()
	_, err = HandleInitCommand(claudeDir, "--claude")
	if err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(filepath.Join(claudeDir, ".claude", "CLAUDE.md"))
	if err != nil || !strings.HasPrefix(string(body), "# CLAUDE.md") {
		t.Fatalf("CLAUDE.md body = %q, %v", body, err)
	}
}

func TestHandleMemoryEditCreatesGenDefaultsForEmptyScopes(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)

	global, err := handleMemoryEdit(cwd, "global")
	if err != nil || global != filepath.Join(home, ".gen", "GEN.md") {
		t.Fatalf("global default = %q, %v", global, err)
	}
	local, err := handleMemoryEdit(cwd, "local")
	if err != nil || local != filepath.Join(cwd, ".gen", "GEN.local.md") {
		t.Fatalf("local default = %q, %v", local, err)
	}
}

func writeMemoryTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
