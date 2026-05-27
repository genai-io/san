// Package instruction discovers and loads mutable user/project instruction
// documents from Gen Code and compatible CLI formats.
package instruction

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/genai-io/gen-code/internal/log"
	"go.uber.org/zap"
)

const (
	maxImportDepth = 5

	FormatGen    = "gen"
	FormatClaude = "claude"
	FormatCodex  = "codex"
)

// Format describes one supported primary instruction document family.
// Registration order defines fallback precedence.
type Format struct {
	ID              string
	GlobalPaths     func(home string) []string
	ProjectPaths    func(cwd string) []string
	NewProjectPath  func(cwd string) string
	ProjectTemplate func(projectName string) string
}

var formats = []Format{
	{
		ID: FormatGen,
		GlobalPaths: func(home string) []string {
			return []string{filepath.Join(home, ".gen", "GEN.md")}
		},
		ProjectPaths: func(cwd string) []string {
			return []string{filepath.Join(cwd, ".gen", "GEN.md"), filepath.Join(cwd, "GEN.md")}
		},
		NewProjectPath: func(cwd string) string {
			return filepath.Join(cwd, ".gen", "GEN.md")
		},
		ProjectTemplate: func(projectName string) string {
			return projectTemplate("GEN.md", "GenCode", projectName)
		},
	},
	{
		ID: FormatClaude,
		GlobalPaths: func(home string) []string {
			return []string{filepath.Join(home, ".claude", "CLAUDE.md")}
		},
		ProjectPaths: func(cwd string) []string {
			return []string{filepath.Join(cwd, ".claude", "CLAUDE.md"), filepath.Join(cwd, "CLAUDE.md")}
		},
		NewProjectPath: func(cwd string) string {
			return filepath.Join(cwd, ".claude", "CLAUDE.md")
		},
		ProjectTemplate: func(projectName string) string {
			return projectTemplate("CLAUDE.md", "Claude Code and GenCode", projectName)
		},
	},
	{
		ID: FormatCodex,
		GlobalPaths: func(home string) []string {
			return []string{filepath.Join(home, ".codex", "AGENTS.md")}
		},
		ProjectPaths: func(cwd string) []string {
			return []string{filepath.Join(cwd, "AGENTS.md")}
		},
		NewProjectPath: func(cwd string) string {
			return filepath.Join(cwd, "AGENTS.md")
		},
		ProjectTemplate: func(projectName string) string {
			return projectTemplate("AGENTS.md", "Codex and GenCode", projectName)
		},
	},
}

// File represents one loaded instruction or rules file with metadata.
type File struct {
	Path    string
	Size    int64
	Content string
	Level   string // "global", "project", or "local"
}

// Paths holds categorized instruction file candidates.
type Paths struct {
	Global       []string
	GlobalRules  string
	Project      []string
	ProjectRules string
	Local        []string
}

// Formats returns the registered primary instruction formats in precedence order.
func Formats() []Format {
	return append([]Format(nil), formats...)
}

// ProjectFile returns the new project instruction path for a format.
func ProjectFile(cwd, formatID string) (string, bool) {
	for _, format := range formats {
		if format.ID == formatID {
			return format.NewProjectPath(cwd), true
		}
	}
	return "", false
}

// ProjectTemplate renders a new project instruction document for a format.
func ProjectTemplate(cwd, formatID string) (string, bool) {
	for _, format := range formats {
		if format.ID == formatID {
			return format.ProjectTemplate(filepath.Base(cwd)), true
		}
	}
	return "", false
}

// GlobalTemplate renders the Gen Code-native global instruction template.
func GlobalTemplate() string {
	return `# GEN.md

Global instructions for GenCode (applies to all projects).

## Coding Preferences

<!-- Your preferred coding style -->

## Security

<!-- Security practices to follow -->
`
}

// LocalTemplate renders the Gen Code-native local project instruction template.
func LocalTemplate() string {
	return `# GEN.local.md

Local instructions for this project (not committed to git).

Use this file for:
- Personal notes and reminders
- Environment-specific settings
- Credentials and secrets (keep these safe!)
- Work-in-progress ideas

## Notes

<!-- Your local notes here -->
`
}

// RulesTemplate renders an example Gen Code rules document.
func RulesTemplate() string {
	return `# Example Rule

This file defines specific rules for GenCode to follow.

## Guidelines

- Add specific guidelines here
- Each rule file should focus on one topic
- Rules are loaded alphabetically by filename

## Example

<!-- Remove this example and add your actual rules -->
`
}

func projectTemplate(fileName, consumer, projectName string) string {
	return fmt.Sprintf(`# %s

This file provides guidance to %s when working with code in this repository.

## Project Overview

%s - Describe what this project does.

## Build & Run

`+"`"+`bash
# Add your build commands here
`+"`"+`

## Architecture

<!-- Key directories and their purpose -->

## Key Patterns

<!-- Important conventions to follow -->
`, fileName, consumer, projectName)
}

// Load returns user-level and project-level instruction bodies separately.
func Load(cwd string) (user, project string) {
	files := LoadFiles(cwd)
	var userParts, projectParts []string
	for _, f := range files {
		switch f.Level {
		case "global":
			userParts = append(userParts, f.Content)
		case "project", "local":
			projectParts = append(projectParts, f.Content)
		}
	}
	return strings.Join(userParts, "\n\n"), strings.Join(projectParts, "\n\n")
}

// LoadFiles loads primary, rules, and local instruction documents.
// Primary documents are fallback alternatives; only the first non-empty
// existing file in the registered precedence order is loaded per scope.
func LoadFiles(cwd string) []File {
	var files []File
	seen := make(map[string]bool)
	paths := AllPaths(cwd)

	if f := loadFile(paths.Global, "global", seen); f != nil {
		files = append(files, *f)
	}
	files = append(files, loadRulesDirectory(paths.GlobalRules, "global", seen)...)

	if f := loadFile(paths.Project, "project", seen); f != nil {
		files = append(files, *f)
	}
	files = append(files, loadRulesDirectory(paths.ProjectRules, "project", seen)...)

	if f := loadFile(paths.Local, "local", seen); f != nil {
		files = append(files, *f)
	}

	return files
}

// AllPaths returns supported instruction paths in discovery order.
func AllPaths(cwd string) Paths {
	homeDir, _ := os.UserHomeDir()
	var global, project []string
	for _, format := range formats {
		global = append(global, format.GlobalPaths(homeDir)...)
		project = append(project, format.ProjectPaths(cwd)...)
	}
	return Paths{
		Global:       global,
		GlobalRules:  filepath.Join(homeDir, ".gen", "rules"),
		Project:      project,
		ProjectRules: filepath.Join(cwd, ".gen", "rules"),
		Local:        []string{filepath.Join(cwd, ".gen", "GEN.local.md")},
	}
}

func loadFile(sources []string, level string, seen map[string]bool) *File {
	for _, src := range sources {
		info, err := os.Stat(src)
		if err != nil || seen[src] {
			continue
		}
		data, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		content := strings.TrimSpace(string(data))
		if content == "" {
			continue
		}
		seen[src] = true
		content = resolveImports(content, filepath.Dir(src), 0, seen)

		log.Logger().Info("Loaded instruction file",
			zap.String("path", src),
			zap.Int64("bytes", info.Size()),
			zap.String("level", level))

		return &File{
			Path:    src,
			Size:    info.Size(),
			Content: fmt.Sprintf("<!-- Source: %s -->\n%s", src, content),
			Level:   level,
		}
	}
	return nil
}

func loadRulesDirectory(dir string, level string, seen map[string]bool) []File {
	var files []File
	for _, path := range ListRulesFiles(dir) {
		if f := loadFile([]string{path}, level, seen); f != nil {
			files = append(files, *f)
		}
	}
	return files
}

// importRe matches @import directives in instruction files (e.g., @file.md).
var importRe = regexp.MustCompile(`(?m)^@([^\s@]+\.md)\s*$`)

func resolveImports(content, basePath string, depth int, seen map[string]bool) string {
	if depth >= maxImportDepth {
		return content
	}
	return importRe.ReplaceAllStringFunc(content, func(match string) string {
		importPath := strings.TrimPrefix(strings.TrimSpace(match), "@")
		fullPath := filepath.Clean(filepath.Join(basePath, importPath))
		baseWithSep := basePath + string(filepath.Separator)
		if fullPath != basePath && !strings.HasPrefix(fullPath, baseWithSep) {
			return fmt.Sprintf("<!-- Import blocked (outside base): @%s -->", importPath)
		}
		if realPath, err := filepath.EvalSymlinks(fullPath); err == nil {
			realBase, _ := filepath.EvalSymlinks(basePath)
			if realBase != "" {
				realBaseWithSep := realBase + string(filepath.Separator)
				if realPath != realBase && !strings.HasPrefix(realPath, realBaseWithSep) {
					return fmt.Sprintf("<!-- Import blocked (symlink escape): @%s -->", importPath)
				}
			}
		}
		if seen[fullPath] {
			return fmt.Sprintf("<!-- Skipped (cycle): @%s -->", importPath)
		}
		data, err := os.ReadFile(fullPath)
		if err != nil {
			return fmt.Sprintf("<!-- Import not found: @%s -->", importPath)
		}
		seen[fullPath] = true
		importedContent := strings.TrimSpace(string(data))
		log.Logger().Info("Resolved import",
			zap.String("import", importPath),
			zap.String("fullPath", fullPath),
			zap.Int("depth", depth))
		importedContent = resolveImports(importedContent, filepath.Dir(fullPath), depth+1, seen)
		return fmt.Sprintf("<!-- Imported: %s -->\n%s", importPath, importedContent)
	})
}

// FindActiveFile returns the first readable, non-empty file in precedence
// order, matching the primary document selection rule used by LoadFiles.
func FindActiveFile(paths []string) string {
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err == nil && strings.TrimSpace(string(data)) != "" {
			return path
		}
	}
	return ""
}

// FindExisting returns the first existing path, including empty draft files.
// Creation/edit flows use it only when no active file was discovered.
func FindExisting(paths []string) string {
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

// ListRulesFiles returns non-empty .md files from a rules directory in lexical
// order, matching the files LoadFiles includes in rendered instructions.
func ListRulesFiles(rulesDir string) []string {
	entries, err := os.ReadDir(rulesDir)
	if err != nil {
		return nil
	}
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
			path := filepath.Join(rulesDir, entry.Name())
			if FindActiveFile([]string{path}) != "" {
				files = append(files, path)
			}
		}
	}
	sort.Strings(files)
	return files
}

// FileSize returns the size of a file in bytes, or zero if absent.
func FileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// FormatFileSize renders a compact size string for UI output.
func FormatFileSize(size int64) string {
	if size >= 1024*1024 {
		return fmt.Sprintf("%.1fMB", float64(size)/(1024*1024))
	}
	if size >= 1024 {
		return fmt.Sprintf("%.1fKB", float64(size)/1024)
	}
	return fmt.Sprintf("%dB", size)
}
