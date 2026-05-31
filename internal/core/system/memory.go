package system

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/genai-io/gen-code/internal/log"
	"go.uber.org/zap"
)

const (
	maxImportDepth = 5

	// AutoMemoryIndexName is the index file of the agent-written auto-memory
	// store. Topic files (loaded on demand by the agent) live beside it.
	AutoMemoryIndexName = "MEMORY.md"

	// autoMemoryByteCap bounds how much of the auto-memory index is injected at
	// session start, mirroring Claude Code's index cap. Topic files are never
	// injected — the agent reads them on demand.
	autoMemoryByteCap = 25 * 1024
)

// AutoMemoryDir is the project-partitioned directory backing the agent-written
// auto-memory store: ~/.gen/projects/<encoded-cwd>/memory/. It shares the
// project partitioning used by the session transcript store, so worktrees and
// subdirectories of one repo resolve to the same store.
func AutoMemoryDir(cwd string) string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(cwd, ".gen", "memory")
	}
	return filepath.Join(homeDir, ".gen", "projects", encodeProjectPath(cwd), "memory")
}

// encodeProjectPath mirrors internal/session.EncodePath: replaces path
// separators with "-" so the cwd can stand alone as a subdirectory name.
// Duplicated (5 lines) to keep core layer-pure rather than importing the
// session feature package; the two functions must stay in lockstep so
// memory and transcript stores resolve to the same project partition.
func encodeProjectPath(path string) string {
	path = strings.TrimRight(path, "/")
	if runtime.GOOS == "windows" {
		path = strings.ReplaceAll(path, ":", "-")
		path = strings.ReplaceAll(path, "\\", "-")
	}
	return strings.ReplaceAll(path, "/", "-")
}

// AutoMemoryIndexPath is the auto-memory index file for cwd's project.
func AutoMemoryIndexPath(cwd string) string {
	return filepath.Join(AutoMemoryDir(cwd), AutoMemoryIndexName)
}

// LoadAutoMemory reads the agent-written auto-memory index for cwd, capped at
// autoMemoryByteCap. It is a distinct source from LoadMemoryFiles: agent-written
// memory and user-authored GEN.md/CLAUDE.md instructions are injected as
// separate blocks and never mixed. Returns ("", false) when the store is empty
// or absent. When the index exceeds the cap it is truncated on a line boundary
// with a marker — topic files are read on demand and never injected.
func LoadAutoMemory(cwd string) (string, bool) {
	data, err := os.ReadFile(AutoMemoryIndexPath(cwd))
	if err != nil {
		return "", false
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return "", false
	}
	if len(content) > autoMemoryByteCap {
		content = truncateOnLineBoundary(content, autoMemoryByteCap) +
			"\n\n<!-- auto-memory truncated; read topic files on demand -->"
	}
	return content, true
}

// truncateOnLineBoundary trims s to at most max bytes, cutting at the last
// newline within the budget so a partial line is never injected.
func truncateOnLineBoundary(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := s[:max]
	if i := strings.LastIndexByte(cut, '\n'); i > 0 {
		return cut[:i]
	}
	return cut
}

// MemoryFile represents a loaded memory file with metadata.
type MemoryFile struct {
	Path    string
	Size    int64
	Content string
	Level   string // "global", "project", or "local"
}

// LoadInstructions loads user-level and project-level instructions separately.
func LoadInstructions(cwd string) (user, project string) {
	files := LoadMemoryFiles(cwd)
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

// LoadMemoryFiles loads all memory files with metadata.
// Returns files in order: global, global rules, project, project rules, local.
func LoadMemoryFiles(cwd string) []MemoryFile {
	var files []MemoryFile
	homeDir, _ := os.UserHomeDir()
	seen := make(map[string]bool)

	userSources := []string{
		filepath.Join(homeDir, ".gen", "GEN.md"),
		filepath.Join(homeDir, ".claude", "CLAUDE.md"),
	}
	if f := loadMemoryFile(userSources, "global", seen); f != nil {
		files = append(files, *f)
	}

	userRulesDir := filepath.Join(homeDir, ".gen", "rules")
	files = append(files, loadRulesDirectory(userRulesDir, "global", seen)...)

	projectSources := []string{
		filepath.Join(cwd, ".gen", "GEN.md"),
		filepath.Join(cwd, "GEN.md"),
		filepath.Join(cwd, ".claude", "CLAUDE.md"),
		filepath.Join(cwd, "CLAUDE.md"),
	}
	if f := loadMemoryFile(projectSources, "project", seen); f != nil {
		files = append(files, *f)
	}

	projectRulesDir := filepath.Join(cwd, ".gen", "rules")
	files = append(files, loadRulesDirectory(projectRulesDir, "project", seen)...)

	localSources := []string{
		filepath.Join(cwd, ".gen", "GEN.local.md"),
	}
	if f := loadMemoryFile(localSources, "local", seen); f != nil {
		files = append(files, *f)
	}

	return files
}

func loadMemoryFile(sources []string, level string, seen map[string]bool) *MemoryFile {
	for _, src := range sources {
		info, err := os.Stat(src)
		if err != nil {
			continue
		}
		if seen[src] {
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

		log.Logger().Info("Loaded memory file",
			zap.String("path", src),
			zap.Int64("bytes", info.Size()),
			zap.String("level", level))

		return &MemoryFile{
			Path:    src,
			Size:    info.Size(),
			Content: fmt.Sprintf("<!-- Source: %s -->\n%s", src, content),
			Level:   level,
		}
	}
	return nil
}

func loadRulesDirectory(dir string, level string, seen map[string]bool) []MemoryFile {
	var files []MemoryFile
	entries, err := os.ReadDir(dir)
	if err != nil {
		return files
	}
	var mdFiles []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(strings.ToLower(name), ".md") {
			mdFiles = append(mdFiles, filepath.Join(dir, name))
		}
	}
	sort.Strings(mdFiles)
	for _, path := range mdFiles {
		if f := loadMemoryFile([]string{path}, level, seen); f != nil {
			files = append(files, *f)
		}
	}
	return files
}

// importRe matches @import directives in memory files (e.g., @file.md).
var importRe = regexp.MustCompile(`(?m)^@([^\s@]+\.md)\s*$`)

func resolveImports(content string, basePath string, depth int, seen map[string]bool) string {
	if depth >= maxImportDepth {
		return content
	}
	return importRe.ReplaceAllStringFunc(content, func(match string) string {
		importPath := strings.TrimPrefix(strings.TrimSpace(match), "@")
		fullPath := filepath.Clean(filepath.Join(basePath, importPath))

		// Path traversal guard: resolved path must stay under basePath.
		// Use trailing separator to prevent prefix collisions (e.g., /tmp/project vs /tmp/projectile).
		baseWithSep := basePath + string(filepath.Separator)
		if fullPath != basePath && !strings.HasPrefix(fullPath, baseWithSep) {
			return fmt.Sprintf("<!-- Import blocked (outside base): @%s -->", importPath)
		}

		// Symlink guard: resolve symlinks and re-check to prevent escapes
		// via symlinks that point outside the base directory.
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

// MemoryPaths holds categorized memory file paths.
type MemoryPaths struct {
	Global       []string
	GlobalRules  string
	Project      []string
	ProjectRules string
	Local        []string
}

// GetAllMemoryPaths returns all memory paths organized by category.
func GetAllMemoryPaths(cwd string) MemoryPaths {
	homeDir, _ := os.UserHomeDir()
	return MemoryPaths{
		Global: []string{
			filepath.Join(homeDir, ".gen", "GEN.md"),
			filepath.Join(homeDir, ".claude", "CLAUDE.md"),
		},
		GlobalRules: filepath.Join(homeDir, ".gen", "rules"),
		Project: []string{
			filepath.Join(cwd, ".gen", "GEN.md"),
			filepath.Join(cwd, "GEN.md"),
			filepath.Join(cwd, ".claude", "CLAUDE.md"),
			filepath.Join(cwd, "CLAUDE.md"),
		},
		ProjectRules: filepath.Join(cwd, ".gen", "rules"),
		Local: []string{
			filepath.Join(cwd, ".gen", "GEN.local.md"),
		},
	}
}

// FindMemoryFile returns the first existing file path from the given list.
func FindMemoryFile(paths []string) string {
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

// ListRulesFiles returns all .md files in a rules directory.
func ListRulesFiles(rulesDir string) []string {
	entries, err := os.ReadDir(rulesDir)
	if err != nil {
		return nil
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(strings.ToLower(name), ".md") {
			files = append(files, filepath.Join(rulesDir, name))
		}
	}
	sort.Strings(files)
	return files
}

// GetFileSize returns the size of a file in bytes, or 0 if not found.
func GetFileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// FormatFileSize formats a file size for display.
func FormatFileSize(size int64) string {
	if size >= 1024*1024 {
		return fmt.Sprintf("%.1fMB", float64(size)/(1024*1024))
	}
	if size >= 1024 {
		return fmt.Sprintf("%.1fKB", float64(size)/1024)
	}
	return fmt.Sprintf("%dB", size)
}
