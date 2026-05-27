package instruction

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAllPathsPrecedence(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)

	paths := AllPaths(cwd)
	wantGlobal := []string{
		filepath.Join(home, ".gen", "GEN.md"),
		filepath.Join(home, ".claude", "CLAUDE.md"),
		filepath.Join(home, ".codex", "AGENTS.md"),
	}
	wantProject := []string{
		filepath.Join(cwd, ".gen", "GEN.md"),
		filepath.Join(cwd, "GEN.md"),
		filepath.Join(cwd, ".claude", "CLAUDE.md"),
		filepath.Join(cwd, "CLAUDE.md"),
		filepath.Join(cwd, "AGENTS.md"),
	}
	assertPaths(t, paths.Global, wantGlobal)
	assertPaths(t, paths.Project, wantProject)
}

func TestLoadFilesFallsBackToCodexAndPrefersClaude(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)

	writeFile(t, filepath.Join(home, ".codex", "AGENTS.md"), "global codex")
	writeFile(t, filepath.Join(cwd, "AGENTS.md"), "project codex")

	files := LoadFiles(cwd)
	if len(files) != 2 || !strings.Contains(files[0].Content, "global codex") ||
		!strings.Contains(files[1].Content, "project codex") {
		t.Fatalf("expected Codex fallback files, got %+v", files)
	}

	writeFile(t, filepath.Join(cwd, "CLAUDE.md"), "project claude")
	files = LoadFiles(cwd)
	if !strings.Contains(files[1].Content, "project claude") ||
		strings.Contains(files[1].Content, "project codex") {
		t.Fatalf("expected CLAUDE.md to take precedence over AGENTS.md, got %q", files[1].Content)
	}
}

func TestFindActiveFileSkipsEmptyHigherPrecedenceCandidate(t *testing.T) {
	cwd := t.TempDir()
	writeFile(t, filepath.Join(cwd, "CLAUDE.md"), " \n")
	writeFile(t, filepath.Join(cwd, "AGENTS.md"), "project codex")

	paths := AllPaths(cwd)
	if got := FindActiveFile(paths.Project); got != filepath.Join(cwd, "AGENTS.md") {
		t.Fatalf("FindActiveFile(project) = %q, want AGENTS.md", got)
	}
	if got := FindExisting(paths.Project); got != filepath.Join(cwd, "CLAUDE.md") {
		t.Fatalf("FindExisting(project) = %q, want the existing empty draft", got)
	}
}

func TestLoadFilesPrefersGenAndPreservesRulesLocalOrder(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)

	writeFile(t, filepath.Join(home, ".gen", "GEN.md"), "global gen")
	writeFile(t, filepath.Join(home, ".claude", "CLAUDE.md"), "global claude")
	writeFile(t, filepath.Join(home, ".gen", "rules", "01-global.md"), "global rule")
	writeFile(t, filepath.Join(cwd, ".gen", "GEN.md"), "project gen")
	writeFile(t, filepath.Join(cwd, "AGENTS.md"), "project codex")
	writeFile(t, filepath.Join(cwd, ".gen", "rules", "01-project.md"), "project rule")
	writeFile(t, filepath.Join(cwd, ".gen", "GEN.local.md"), "project local")

	files := LoadFiles(cwd)
	if len(files) != 5 {
		t.Fatalf("expected five selected instruction files, got %d: %+v", len(files), files)
	}
	for i, want := range []string{"global gen", "global rule", "project gen", "project rule", "project local"} {
		if !strings.Contains(files[i].Content, want) {
			t.Errorf("file[%d] = %q, want content %q", i, files[i].Content, want)
		}
	}
}

func TestLoadFilesResolvesImportsAndCycles(t *testing.T) {
	cwd := t.TempDir()
	writeFile(t, filepath.Join(cwd, ".gen", "GEN.md"), "# Root\n@a.md")
	writeFile(t, filepath.Join(cwd, ".gen", "a.md"), "A\n@GEN.md")

	files := LoadFiles(cwd)
	var project File
	for _, file := range files {
		if file.Level == "project" {
			project = file
		}
	}
	if !strings.Contains(project.Content, "<!-- Imported: a.md -->") ||
		!strings.Contains(project.Content, "Skipped (cycle)") {
		t.Fatalf("expected imported content with cycle suppression, got %q", project.Content)
	}
}

func TestLoadFilesBlocksImportOutsideDocumentDirectory(t *testing.T) {
	cwd := t.TempDir()
	writeFile(t, filepath.Join(cwd, ".gen", "GEN.md"), "@../outside.md")
	writeFile(t, filepath.Join(cwd, "outside.md"), "must not load")

	files := LoadFiles(cwd)
	if !strings.Contains(files[0].Content, "Import blocked (outside base)") ||
		strings.Contains(files[0].Content, "must not load") {
		t.Fatalf("outside import was not blocked: %q", files[0].Content)
	}
}

func TestListRulesFilesIsSortedAndMarkdownOnly(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "z.md"), "z")
	writeFile(t, filepath.Join(dir, "a.md"), "a")
	writeFile(t, filepath.Join(dir, "empty.md"), "\n")
	writeFile(t, filepath.Join(dir, "ignored.txt"), "ignored")

	files := ListRulesFiles(dir)
	want := []string{filepath.Join(dir, "a.md"), filepath.Join(dir, "z.md")}
	assertPaths(t, files, want)
}

func TestProjectFileAndTemplate(t *testing.T) {
	cwd := filepath.Join("/tmp", "demo")
	path, ok := ProjectFile(cwd, FormatCodex)
	if !ok || path != filepath.Join(cwd, "AGENTS.md") {
		t.Fatalf("ProjectFile(codex) = %q, %v", path, ok)
	}
	body, ok := ProjectTemplate(cwd, FormatClaude)
	if !ok || !strings.HasPrefix(body, "# CLAUDE.md") {
		t.Fatalf("ProjectTemplate(claude) = %q, %v", body, ok)
	}
}

func TestLoadIncludesLocalInstructions(t *testing.T) {
	cwd := t.TempDir()
	writeFile(t, filepath.Join(cwd, ".gen", "GEN.md"), "project")
	writeFile(t, filepath.Join(cwd, ".gen", "GEN.local.md"), "local")
	_, project := Load(cwd)
	if !strings.Contains(project, "project") || !strings.Contains(project, "local") {
		t.Fatalf("project instructions = %q", project)
	}
}

func TestFormatFileSize(t *testing.T) {
	for _, tc := range []struct {
		size int64
		want string
	}{{500, "500B"}, {1024, "1.0KB"}, {1024 * 1024, "1.0MB"}} {
		if got := FormatFileSize(tc.size); got != tc.want {
			t.Errorf("FormatFileSize(%d) = %q, want %q", tc.size, got, tc.want)
		}
	}
}

func assertPaths(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("paths = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("paths[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
