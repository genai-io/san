package system

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveImports(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "san-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	mainContent := `# Main File
@imported.md
Some content after import`

	importedContent := `## Imported Content
This was imported from another file.`

	if err := os.WriteFile(filepath.Join(tmpDir, "main.md"), []byte(mainContent), 0o644); err != nil {
		t.Fatalf("Failed to write main.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "imported.md"), []byte(importedContent), 0o644); err != nil {
		t.Fatalf("Failed to write imported.md: %v", err)
	}

	seen := make(map[string]bool)
	result := resolveImports(mainContent, tmpDir, 0, seen)

	if !strings.Contains(result, "<!-- Imported: imported.md -->") {
		t.Errorf("Expected import comment, got: %s", result)
	}
	if !strings.Contains(result, "This was imported from another file.") {
		t.Errorf("Expected imported content, got: %s", result)
	}
	if !strings.Contains(result, "Some content after import") {
		t.Errorf("Expected content after import, got: %s", result)
	}
}

func TestResolveImportsCycle(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "san-test-cycle")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	file1Content := `# File 1
@file2.md`

	file2Content := `# File 2
@file1.md`

	if err := os.WriteFile(filepath.Join(tmpDir, "file1.md"), []byte(file1Content), 0o644); err != nil {
		t.Fatalf("Failed to write file1.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "file2.md"), []byte(file2Content), 0o644); err != nil {
		t.Fatalf("Failed to write file2.md: %v", err)
	}

	seen := make(map[string]bool)
	seen[filepath.Join(tmpDir, "file1.md")] = true
	result := resolveImports(file1Content, tmpDir, 0, seen)

	if !strings.Contains(result, "# File 2") {
		t.Errorf("Expected file2 content, got: %s", result)
	}
	if !strings.Contains(result, "Skipped (cycle)") {
		t.Errorf("Expected cycle skip comment, got: %s", result)
	}
}

func TestResolveImportsNotFound(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "san-test-notfound")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	content := `# Test
@nonexistent.md`

	seen := make(map[string]bool)
	result := resolveImports(content, tmpDir, 0, seen)

	if !strings.Contains(result, "Import not found") {
		t.Errorf("Expected not found comment, got: %s", result)
	}
}

func TestResolveImportsMaxDepth(t *testing.T) {
	content := `@deep.md`

	seen := make(map[string]bool)
	result := resolveImports(content, "/tmp", maxImportDepth, seen)

	if result != content {
		t.Errorf("Expected unchanged content at max depth, got: %s", result)
	}
}

func TestLoadRulesDirectory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "san-test-rules")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	rulesDir := filepath.Join(tmpDir, "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatalf("Failed to create rules dir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(rulesDir, "coding.md"), []byte("# Coding Rules"), 0o644); err != nil {
		t.Fatalf("Failed to write coding.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "security.md"), []byte("# Security Rules"), 0o644); err != nil {
		t.Fatalf("Failed to write security.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "readme.txt"), []byte("Ignore me"), 0o644); err != nil {
		t.Fatalf("Failed to write readme.txt: %v", err)
	}

	seen := make(map[string]bool)
	files := loadRulesDirectory(rulesDir, "project", seen)

	if len(files) != 2 {
		t.Errorf("Expected 2 rule files, got %d", len(files))
	}

	if len(files) > 0 && !strings.Contains(files[0].Path, "coding.md") {
		t.Errorf("Expected coding.md first (alphabetical), got: %s", files[0].Path)
	}
	if len(files) > 1 && !strings.Contains(files[1].Path, "security.md") {
		t.Errorf("Expected security.md second, got: %s", files[1].Path)
	}
}

func TestGetAllMemoryPaths(t *testing.T) {
	cwd := "/test/project"
	paths := GetAllMemoryPaths(cwd)

	// SAN.md (+ root), the pre-rename GEN.md (+ root), and CLAUDE.md (+ .claude).
	if len(paths.Project) != 6 {
		t.Errorf("Expected 6 project paths, got %d", len(paths.Project))
	}
	if !strings.Contains(paths.Project[0], "SAN.md") {
		t.Errorf("Expected SAN.md preferred first, got: %s", paths.Project[0])
	}

	// SAN.local.md preferred, with the pre-rename GEN.local.md as fallback.
	if len(paths.Local) != 2 {
		t.Errorf("Expected 2 local paths, got %d", len(paths.Local))
	}
	if !strings.Contains(paths.Local[0], "SAN.local.md") {
		t.Errorf("Expected SAN.local.md preferred in local paths, got: %s", paths.Local[0])
	}
	if !strings.Contains(paths.Local[1], "GEN.local.md") {
		t.Errorf("Expected GEN.local.md fallback in local paths, got: %s", paths.Local[1])
	}

	if !strings.Contains(paths.ProjectRules, "rules") {
		t.Errorf("Expected rules in project rules path, got: %s", paths.ProjectRules)
	}
}

func TestFormatFileSize(t *testing.T) {
	tests := []struct {
		size     int64
		expected string
	}{
		{500, "500B"},
		{1024, "1.0KB"},
		{2048, "2.0KB"},
		{1024 * 1024, "1.0MB"},
		{1536 * 1024, "1.5MB"},
	}

	for _, tc := range tests {
		result := FormatFileSize(tc.size)
		if result != tc.expected {
			t.Errorf("FormatFileSize(%d) = %s, expected %s", tc.size, result, tc.expected)
		}
	}
}

func TestResolveImportsNested(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "san-test-nested")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	aContent := `# Level A
@b.md
After B import`

	bContent := `## Level B
@c.md
After C import`

	cContent := `### Level C
Deepest content`

	if err := os.WriteFile(filepath.Join(tmpDir, "a.md"), []byte(aContent), 0o644); err != nil {
		t.Fatalf("Failed to write a.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "b.md"), []byte(bContent), 0o644); err != nil {
		t.Fatalf("Failed to write b.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "c.md"), []byte(cContent), 0o644); err != nil {
		t.Fatalf("Failed to write c.md: %v", err)
	}

	seen := make(map[string]bool)
	result := resolveImports(aContent, tmpDir, 0, seen)

	if !strings.Contains(result, "<!-- Imported: b.md -->") {
		t.Errorf("Expected b.md import comment, got: %s", result)
	}
	if !strings.Contains(result, "<!-- Imported: c.md -->") {
		t.Errorf("Expected c.md import comment, got: %s", result)
	}
	if !strings.Contains(result, "Deepest content") {
		t.Errorf("Expected deepest content from c.md, got: %s", result)
	}
	if !strings.Contains(result, "After C import") {
		t.Errorf("Expected content after C import from b.md, got: %s", result)
	}
	if !strings.Contains(result, "After B import") {
		t.Errorf("Expected content after B import from a.md, got: %s", result)
	}
}

func TestResolveImportsRelativePath(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "san-test-relative")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	subDir := filepath.Join(tmpDir, "subdir")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("Failed to create subdir: %v", err)
	}

	mainContent := `# Main
@./subdir/nested.md`

	nestedContent := `## Nested
Nested content here`

	if err := os.WriteFile(filepath.Join(tmpDir, "main.md"), []byte(mainContent), 0o644); err != nil {
		t.Fatalf("Failed to write main.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "nested.md"), []byte(nestedContent), 0o644); err != nil {
		t.Fatalf("Failed to write nested.md: %v", err)
	}

	seen := make(map[string]bool)
	result := resolveImports(mainContent, tmpDir, 0, seen)

	if !strings.Contains(result, "<!-- Imported: ./subdir/nested.md -->") {
		t.Errorf("Expected nested import comment, got: %s", result)
	}
	if !strings.Contains(result, "Nested content here") {
		t.Errorf("Expected nested content, got: %s", result)
	}
}

func TestLoadMemoryFilesWithImports(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "san-test-memory-imports")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	genDir := filepath.Join(tmpDir, ".gen")
	if err := os.MkdirAll(genDir, 0o755); err != nil {
		t.Fatalf("Failed to create .gen dir: %v", err)
	}

	genMdContent := `# Project Memory
@extra.md
End of memory`

	extraContent := `## Extra Content
This was imported`

	if err := os.WriteFile(filepath.Join(genDir, "GEN.md"), []byte(genMdContent), 0o644); err != nil {
		t.Fatalf("Failed to write GEN.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(genDir, "extra.md"), []byte(extraContent), 0o644); err != nil {
		t.Fatalf("Failed to write extra.md: %v", err)
	}

	files := LoadMemoryFiles(tmpDir)

	var projectFile *MemoryFile
	for i := range files {
		if files[i].Level == "project" && strings.Contains(files[i].Path, "GEN.md") {
			projectFile = &files[i]
			break
		}
	}

	if projectFile == nil {
		t.Fatal("Expected to find project GEN.md file")
	}

	if !strings.Contains(projectFile.Content, "<!-- Imported: extra.md -->") {
		t.Errorf("Expected import comment in content, got: %s", projectFile.Content)
	}
	if !strings.Contains(projectFile.Content, "This was imported") {
		t.Errorf("Expected imported content, got: %s", projectFile.Content)
	}
}

func TestFindMemoryFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "san-test-find")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	existingFile := filepath.Join(tmpDir, "exists.md")
	if err := os.WriteFile(existingFile, []byte("content"), 0o644); err != nil {
		t.Fatalf("Failed to write file: %v", err)
	}

	tests := []struct {
		name     string
		paths    []string
		expected string
	}{
		{
			name:     "first existing file wins",
			paths:    []string{filepath.Join(tmpDir, "notexist.md"), existingFile},
			expected: existingFile,
		},
		{
			name:     "no files exist",
			paths:    []string{filepath.Join(tmpDir, "a.md"), filepath.Join(tmpDir, "b.md")},
			expected: "",
		},
		{
			name:     "empty paths",
			paths:    []string{},
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := FindMemoryFile(tc.paths)
			if result != tc.expected {
				t.Errorf("FindMemoryFile() = %q, expected %q", result, tc.expected)
			}
		})
	}
}

func TestLoadInstructions(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "san-test-instructions")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	genDir := filepath.Join(tmpDir, ".gen")
	if err := os.MkdirAll(genDir, 0o755); err != nil {
		t.Fatalf("Failed to create .gen dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(genDir, "GEN.md"), []byte("Project instructions here"), 0o644); err != nil {
		t.Fatalf("Failed to write GEN.md: %v", err)
	}

	if err := os.WriteFile(filepath.Join(genDir, "GEN.local.md"), []byte("Local instructions here"), 0o644); err != nil {
		t.Fatalf("Failed to write GEN.local.md: %v", err)
	}

	user, project := LoadInstructions(tmpDir)

	if !strings.Contains(project, "Project instructions here") {
		t.Errorf("project instructions should contain GEN.md content, got: %s", project)
	}
	if !strings.Contains(project, "Local instructions here") {
		t.Errorf("project instructions should contain GEN.local.md content, got: %s", project)
	}

	_ = user
}

func TestLoadMemoryFiles_PrefersGenPathsAndPreservesSectionOrder(t *testing.T) {
	tmpHome := t.TempDir()
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpHome)

	userGenDir := filepath.Join(tmpHome, ".gen")
	userClaudeDir := filepath.Join(tmpHome, ".claude")
	projectGenDir := filepath.Join(tmpDir, ".gen")
	projectClaudeDir := filepath.Join(tmpDir, ".claude")

	for _, dir := range []string{userGenDir, userClaudeDir, projectGenDir, projectClaudeDir, filepath.Join(userGenDir, "rules"), filepath.Join(projectGenDir, "rules")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", dir, err)
		}
	}

	if err := os.WriteFile(filepath.Join(userGenDir, "GEN.md"), []byte("user gen"), 0o644); err != nil {
		t.Fatalf("WriteFile(user GEN): %v", err)
	}
	if err := os.WriteFile(filepath.Join(userClaudeDir, "CLAUDE.md"), []byte("user claude fallback"), 0o644); err != nil {
		t.Fatalf("WriteFile(user CLAUDE): %v", err)
	}
	if err := os.WriteFile(filepath.Join(userGenDir, "rules", "01-global.md"), []byte("global rule"), 0o644); err != nil {
		t.Fatalf("WriteFile(global rule): %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectGenDir, "GEN.md"), []byte("project gen"), 0o644); err != nil {
		t.Fatalf("WriteFile(project GEN): %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectClaudeDir, "CLAUDE.md"), []byte("project claude fallback"), 0o644); err != nil {
		t.Fatalf("WriteFile(project CLAUDE): %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectGenDir, "rules", "01-project.md"), []byte("project rule"), 0o644); err != nil {
		t.Fatalf("WriteFile(project rule): %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectGenDir, "GEN.local.md"), []byte("project local"), 0o644); err != nil {
		t.Fatalf("WriteFile(local GEN): %v", err)
	}

	files := LoadMemoryFiles(tmpDir)
	if len(files) != 5 {
		t.Fatalf("expected 5 memory files, got %d", len(files))
	}

	if files[0].Level != "global" || !strings.Contains(files[0].Path, filepath.Join(".gen", "GEN.md")) {
		t.Fatalf("expected global GEN.md first, got level=%q path=%q", files[0].Level, files[0].Path)
	}
	if strings.Contains(files[0].Content, "user claude fallback") {
		t.Fatal("expected user .gen/GEN.md to take precedence over ~/.claude/CLAUDE.md")
	}
	if files[1].Level != "global" {
		t.Fatalf("expected global rules second, got level=%q", files[1].Level)
	}
	if files[2].Level != "project" || !strings.Contains(files[2].Path, filepath.Join(".gen", "GEN.md")) {
		t.Fatalf("expected project GEN.md third, got level=%q path=%q", files[2].Level, files[2].Path)
	}
	if strings.Contains(files[2].Content, "project claude fallback") {
		t.Fatal("expected project .gen/GEN.md to take precedence over project CLAUDE.md")
	}
	if files[3].Level != "project" {
		t.Fatalf("expected project rules fourth, got level=%q", files[3].Level)
	}
	if files[4].Level != "local" || !strings.Contains(files[4].Path, "GEN.local.md") {
		t.Fatalf("expected local GEN.local.md last, got level=%q path=%q", files[4].Level, files[4].Path)
	}
}

func TestMemory_ImportChain(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "san-test-import-chain")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	genDir := filepath.Join(tmpDir, ".gen")
	if err := os.MkdirAll(genDir, 0o755); err != nil {
		t.Fatalf("Failed to create .gen dir: %v", err)
	}

	genMdContent := "# Root\n@a.md"
	aMdContent := "## Level A\n@b.md"
	bMdContent := "### Level B\nFinal content from B"

	if err := os.WriteFile(filepath.Join(genDir, "GEN.md"), []byte(genMdContent), 0o644); err != nil {
		t.Fatalf("Failed to write GEN.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(genDir, "a.md"), []byte(aMdContent), 0o644); err != nil {
		t.Fatalf("Failed to write a.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(genDir, "b.md"), []byte(bMdContent), 0o644); err != nil {
		t.Fatalf("Failed to write b.md: %v", err)
	}

	files := LoadMemoryFiles(tmpDir)

	var projectFile *MemoryFile
	for i := range files {
		if files[i].Level == "project" && strings.Contains(files[i].Path, "GEN.md") {
			projectFile = &files[i]
			break
		}
	}

	if projectFile == nil {
		t.Fatal("Expected to find project GEN.md file")
	}

	if !strings.Contains(projectFile.Content, "Level A") {
		t.Errorf("Expected 'Level A' content (from a.md) in resolved output; got: %s", projectFile.Content)
	}
	if !strings.Contains(projectFile.Content, "Final content from B") {
		t.Errorf("Expected 'Final content from B' (from b.md) in resolved output; got: %s", projectFile.Content)
	}
	if !strings.Contains(projectFile.Content, "<!-- Imported: a.md -->") {
		t.Errorf("Expected import comment for a.md; got: %s", projectFile.Content)
	}
	if !strings.Contains(projectFile.Content, "<!-- Imported: b.md -->") {
		t.Errorf("Expected import comment for b.md; got: %s", projectFile.Content)
	}
}

func TestMemory_MissingFile_NoError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "san-test-missing-genmd")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	files := LoadMemoryFiles(tmpDir)

	for _, f := range files {
		if f.Level == "project" && strings.Contains(f.Path, tmpDir) {
			t.Errorf("Did not expect a project memory file when GEN.md is absent, got: %s", f.Path)
		}
	}

	_, project := LoadInstructions(tmpDir)
	_ = project
}
