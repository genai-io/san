package system

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/genai-io/san/internal/confdir"
	"github.com/genai-io/san/internal/log"
	"go.uber.org/zap"
)

const (
	maxImportDepth = 5

	// InstructionFile is the industry-standard agent instruction file
	// (https://agents.md). San reads it at the user level and at every
	// directory from the project root down to the working directory.
	InstructionFile = "AGENTS.md"

	// LocalInstructionFile holds project instructions that stay out of git.
	LocalInstructionFile = "AGENTS.local.md"

	// instructionsByteCap bounds the combined size of the instruction files
	// injected at session start, matching Codex's 32 KiB project_doc_max_bytes
	// so a deep directory chain cannot crowd out the conversation.
	instructionsByteCap = 32 * 1024

	// AutoMemoryIndexName is the index file of the agent-written auto-memory
	// store: one line per memory, each pointing at a topic file beside it.
	AutoMemoryIndexName = "MEMORY.md"

	// autoMemoryLineCap and autoMemoryByteCap bound how much of the index is
	// injected, matching Claude Code. Topic files are read on demand.
	autoMemoryLineCap = 200
	autoMemoryByteCap = 25 * 1024
)

// AutoMemoryDir is the agent-written memory store for cwd's repository:
// ~/.san/projects/<encoded-repo-root>/memory/. Subdirectories and linked
// worktrees of one repository share it.
func AutoMemoryDir(cwd string) string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(confdir.Dir(cwd), "memory")
	}
	return filepath.Join(confdir.Dir(homeDir), "projects", encodeProjectPath(mainWorktreeRoot(cwd)), "memory")
}

// mainWorktreeRoot returns the repository root for cwd, mapping a linked
// worktree (whose .git is a "gitdir: <main>/.git/worktrees/<name>" file) back
// to the main checkout.
func mainWorktreeRoot(cwd string) string {
	root := ProjectRoot(cwd)
	data, err := os.ReadFile(filepath.Join(root, ".git"))
	if err != nil {
		return root // a directory (main checkout) or no repository
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return root
	}
	gitdir = filepath.Clean(strings.TrimSpace(gitdir))
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(root, gitdir)
	}
	// <main>/.git/worktrees/<name> → <main>
	if filepath.Base(filepath.Dir(gitdir)) == "worktrees" {
		return filepath.Dir(filepath.Dir(filepath.Dir(gitdir)))
	}
	return root
}

// encodeProjectPath replaces path separators with "-" so a path can stand
// alone as a subdirectory name (same scheme as internal/session.EncodePath).
func encodeProjectPath(path string) string {
	path = strings.TrimRight(path, "/")
	if runtime.GOOS == "windows" {
		path = strings.ReplaceAll(path, ":", "-")
		path = strings.ReplaceAll(path, "\\", "-")
	}
	return strings.ReplaceAll(path, "/", "-")
}

// LoadAutoMemoryAt reads the auto-memory index from dir, capped at
// autoMemoryLineCap lines and autoMemoryByteCap bytes, and lists index
// problems (over the cap, topic files missing from the index, index links to
// missing files) for the agent to repair. Both are empty when there is nothing.
func LoadAutoMemoryAt(dir string) (index string, problems []string) {
	data, _ := os.ReadFile(filepath.Join(dir, AutoMemoryIndexName))
	full := strings.TrimSpace(string(data))

	index = full
	if lines := strings.Split(index, "\n"); len(lines) > autoMemoryLineCap {
		index = strings.Join(lines[:autoMemoryLineCap], "\n")
	}
	index = truncateOnLineBoundary(index, autoMemoryByteCap)
	lines := strings.Count(full, "\n") + 1
	switch {
	case len(index) < len(full):
		problems = append(problems, fmt.Sprintf("MEMORY.md is over the %d-line / %d KB load limit and was cut; condense it to one short line per memory.", autoMemoryLineCap, autoMemoryByteCap/1024))
	case lines*10 > autoMemoryLineCap*9 || len(full)*10 > autoMemoryByteCap*9:
		problems = append(problems, fmt.Sprintf("MEMORY.md is near its load limit (%d/%d lines, %d/%d KB); keep one short line per memory and merge or drop stale ones.",
			lines, autoMemoryLineCap, len(full)/1024, autoMemoryByteCap/1024))
	}
	return index, append(problems, autoMemoryIndexProblems(dir, full)...)
}

var markdownLinkTarget = regexp.MustCompile(`\]\(([^)\s]+\.md)\)`)

// autoMemoryIndexProblems reports topic files the index never mentions (the
// agent cannot find them) and index links whose file is gone.
func autoMemoryIndexProblems(dir, index string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var orphans, dangling []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || name == AutoMemoryIndexName || !strings.HasSuffix(name, ".md") {
			continue
		}
		if !strings.Contains(index, name) {
			orphans = append(orphans, name)
		}
	}
	for _, m := range markdownLinkTarget.FindAllStringSubmatch(index, -1) {
		if _, err := os.Stat(filepath.Join(dir, m[1])); err != nil {
			dangling = append(dangling, m[1])
		}
	}
	var problems []string
	if len(orphans) > 0 {
		problems = append(problems, "not listed in MEMORY.md (add a line or delete): "+listNames(orphans))
	}
	if len(dangling) > 0 {
		problems = append(problems, "MEMORY.md links to missing files (drop the line): "+listNames(dangling))
	}
	return problems
}

// listNames joins names, eliding past the first ten.
func listNames(names []string) string {
	if len(names) <= 10 {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:10], ", ") + fmt.Sprintf(" and %d more", len(names)-10)
}

// StampModified records the write time as a `modified` field in the
// frontmatter of the memory file at path, replacing an older one. A file
// without frontmatter is left alone. It reports whether the file changed.
func StampModified(path string, now time.Time) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	src := string(data)
	if !strings.HasPrefix(src, "---\n") {
		return false, nil
	}
	end := strings.Index(src[4:], "\n---")
	if end < 0 {
		return false, nil
	}
	end += 4
	var fm []string
	for _, line := range strings.Split(src[4:end], "\n") {
		if !strings.HasPrefix(line, "modified:") {
			fm = append(fm, line)
		}
	}
	fm = append(fm, "modified: "+now.UTC().Format(time.RFC3339))
	out := "---\n" + strings.Join(fm, "\n") + src[end:]
	if out == src {
		return false, nil
	}
	return true, os.WriteFile(path, []byte(out), 0o644)
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

// LoadMemoryFiles loads every instruction file that applies to cwd, in the
// order they are injected: user-level AGENTS.md, user rules, the project chain
// from the repository root down to cwd, project rules, then the git-ignored
// local override. Files closer to cwd are injected last so their instructions
// win, mirroring the AGENTS.md convention shared by Codex and Cursor.
func LoadMemoryFiles(cwd string) []MemoryFile {
	homeDir, _ := os.UserHomeDir()
	l := &loader{seen: make(map[string]bool), remaining: instructionsByteCap}

	userDir := confdir.Dir(homeDir)
	l.add(filepath.Join(userDir, InstructionFile), "global")
	l.addRulesDirectory(filepath.Join(userDir, "rules"), "global")

	for _, path := range ProjectInstructionChain(cwd) {
		l.add(path, "project")
	}
	l.addRulesDirectory(filepath.Join(confdir.Dir(cwd), "rules"), "project")

	l.add(filepath.Join(ProjectRoot(cwd), LocalInstructionFile), "local")

	return l.files
}

// ProjectRoot returns the repository root at or above cwd, falling back to cwd
// when cwd is not inside a repository. Instruction discovery starts here.
func ProjectRoot(cwd string) string {
	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return cwd
		}
		dir = parent
	}
}

// ProjectInstructionChain returns the AGENTS.md path for every directory from
// the project root down to cwd, root first. Every file in the chain is loaded;
// the ones nearer cwd are injected last and therefore take precedence.
func ProjectInstructionChain(cwd string) []string {
	root := ProjectRoot(cwd)
	paths := []string{filepath.Join(root, InstructionFile)}

	rel, err := filepath.Rel(root, cwd)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return paths
	}
	dir := root
	for _, segment := range strings.Split(rel, string(filepath.Separator)) {
		dir = filepath.Join(dir, segment)
		paths = append(paths, filepath.Join(dir, InstructionFile))
	}
	return paths
}

// loader accumulates instruction files while enforcing the combined byte cap.
type loader struct {
	seen      map[string]bool
	remaining int
	files     []MemoryFile
}

// add loads one instruction file, resolves its @imports, and appends it unless
// the file is missing, empty, already loaded, or the byte cap is exhausted.
func (l *loader) add(path, level string) {
	info, err := os.Stat(path)
	if err != nil || l.seen[path] {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return
	}
	l.seen[path] = true
	content = resolveImports(content, filepath.Dir(path), 0, l.seen)

	if l.remaining <= 0 {
		log.Logger().Warn("Skipped instruction file: byte cap reached",
			zap.String("path", path),
			zap.Int("cap", instructionsByteCap))
		return
	}
	if len(content) > l.remaining {
		log.Logger().Warn("Truncated instruction file: byte cap reached",
			zap.String("path", path),
			zap.Int("cap", instructionsByteCap))
		content = truncateOnLineBoundary(content, l.remaining) +
			"\n\n<!-- instructions truncated: combined 32KB cap reached -->"
	}
	l.remaining -= len(content)

	log.Logger().Info("Loaded instruction file",
		zap.String("path", path),
		zap.Int64("bytes", info.Size()),
		zap.String("level", level))

	l.files = append(l.files, MemoryFile{
		Path:    path,
		Size:    info.Size(),
		Content: fmt.Sprintf("<!-- Source: %s -->\n%s", path, content),
		Level:   level,
	})
}

// addRulesDirectory loads every .md file in dir, alphabetically.
func (l *loader) addRulesDirectory(dir, level string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var paths []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
			continue
		}
		paths = append(paths, filepath.Join(dir, entry.Name()))
	}
	sort.Strings(paths)
	for _, path := range paths {
		l.add(path, level)
	}
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

// MemoryPaths holds the instruction paths San consults, for display and edit.
type MemoryPaths struct {
	Global       string   // ~/.san/AGENTS.md
	GlobalRules  string   // ~/.san/rules/
	Project      []string // <root>/AGENTS.md ... <cwd>/AGENTS.md, root first
	ProjectRules string   // <cwd>/.san/rules/
	Local        string   // <root>/AGENTS.local.md
}

// GetAllMemoryPaths returns all instruction paths organized by category.
func GetAllMemoryPaths(cwd string) MemoryPaths {
	homeDir, _ := os.UserHomeDir()
	userDir := confdir.Dir(homeDir)
	return MemoryPaths{
		Global:       filepath.Join(userDir, InstructionFile),
		GlobalRules:  filepath.Join(userDir, "rules"),
		Project:      ProjectInstructionChain(cwd),
		ProjectRules: filepath.Join(confdir.Dir(cwd), "rules"),
		Local:        filepath.Join(ProjectRoot(cwd), LocalInstructionFile),
	}
}

// FindNearestMemoryFile returns the existing path closest to the working
// directory -- the last entry of a root-first chain -- since the instructions
// nearest cwd are the ones that win.
func FindNearestMemoryFile(paths []string) string {
	for i := len(paths) - 1; i >= 0; i-- {
		if _, err := os.Stat(paths[i]); err == nil {
			return paths[i]
		}
	}
	return ""
}

// ExistingMemoryFiles returns the paths that exist, preserving chain order.
func ExistingMemoryFiles(paths []string) []string {
	var found []string
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			found = append(found, path)
		}
	}
	return found
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
