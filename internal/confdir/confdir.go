// Package confdir resolves the per-user and per-project configuration directory.
//
// confdir is a zero-dependency infrastructure leaf so every layer (including
// internal/log and internal/core) can import it without a layering violation.
package confdir

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Name is the configuration directory name.
const Name = ".san"

// Dir returns the configuration directory under root (root/.san).
func Dir(root string) string {
	return filepath.Join(root, Name)
}

// RepoRoot is the nearest directory at or above cwd holding a .git entry, or
// cwd itself outside a repository.
func RepoRoot(cwd string) string {
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

// MainRepoRoot returns the original checkout when its metadata is available.
// Call ConfigRoot when failure must not silently select another settings source.
func MainRepoRoot(cwd string) string {
	root := RepoRoot(canonical(cwd))
	data, err := os.ReadFile(filepath.Join(root, ".git"))
	if err != nil {
		return canonical(root)
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return canonical(root)
	}
	gitdir = strings.TrimSpace(gitdir)
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(root, gitdir)
	}
	// Normal worktrees need no subprocess on this hot trust/memory path.
	if common, err := os.ReadFile(filepath.Join(gitdir, "commondir")); err == nil {
		dir := strings.TrimSpace(string(common))
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(gitdir, dir)
		}
		dir = canonical(dir)
		if filepath.Base(dir) == ".git" {
			return canonical(filepath.Dir(dir))
		}
		// Separate Git directories cannot identify the checkout by ancestry.
		if main, err := ConfigRoot(cwd); err == nil {
			return main
		}
	}
	return canonical(root)
}

func canonical(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return path
}

// ConfigRoot resolves the original main checkout, not the Git common directory.
// Git's first worktree record identifies it even with --separate-git-dir. Outside
// Git the exact cwd is used, never an ancestor containing configuration files.
// Missing or invalid repository metadata is an error, not a branch-local fallback.
func ConfigRoot(cwd string) (string, error) {
	cwd = canonical(cwd)
	root := RepoRoot(cwd)
	if _, err := os.Stat(filepath.Join(root, ".git")); os.IsNotExist(err) {
		return cwd, nil
	}
	top, err := gitOutput(root, "rev-parse", "--show-toplevel")
	if err != nil || canonical(strings.TrimSpace(string(top))) != canonical(root) {
		return "", fmt.Errorf("invalid repository metadata at %s", root)
	}
	out, err := gitOutput(root, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return "", fmt.Errorf("resolve shared configuration for %s: %w", cwd, err)
	}
	first, _, _ := strings.Cut(string(out), "\x00")
	main, ok := strings.CutPrefix(first, "worktree ")
	if !ok || !filepath.IsAbs(main) {
		return "", fmt.Errorf("resolve shared configuration for %s: invalid main worktree", cwd)
	}
	main = canonical(main)
	common, err := gitOutput(root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("resolve Git common directory for %s: %w", root, err)
	}
	commonDir := canonical(strings.TrimSpace(string(common)))
	// Separate Git directories and submodules can list their metadata directory
	// as the first worktree. core.worktree is based at that common directory.
	if main == commonDir {
		worktree, err := gitOutput(root, "config", "--get", "core.worktree")
		if err != nil || strings.TrimSpace(string(worktree)) == "" {
			return "", fmt.Errorf("shared configuration checkout is ambiguous: %s", main)
		}
		main = strings.TrimSpace(string(worktree))
		if !filepath.IsAbs(main) {
			main = filepath.Join(commonDir, main)
		}
		main = canonical(main)
	}
	if info, err := os.Stat(main); err != nil || !info.IsDir() {
		return "", fmt.Errorf("shared configuration checkout unavailable: %s", main)
	}
	mainTop, topErr := gitOutput(main, "rev-parse", "--show-toplevel")
	if topErr != nil || canonical(strings.TrimSpace(string(mainTop))) != main {
		return "", fmt.Errorf("shared configuration checkout is ambiguous: %s", main)
	}
	mainCommon, mainErr := gitOutput(main, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil || mainErr != nil || canonical(strings.TrimSpace(string(common))) != canonical(strings.TrimSpace(string(mainCommon))) {
		return "", fmt.Errorf("shared configuration checkout no longer belongs to repository: %s", main)
	}
	return main, nil
}

func gitOutput(root string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	// Repository selection must come from cwd, not inherited Git overrides.
	for _, env := range os.Environ() {
		key, _, _ := strings.Cut(env, "=")
		switch key {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE":
			continue
		}
		cmd.Env = append(cmd.Env, env)
	}
	return cmd.Output()
}

// LocalDir holds cwd's personal, untracked config (settings.local.json,
// mcp.local.json): Dir(cwd), or in a linked worktree the same folder in the
// main checkout, so every worktree shares one copy.
func LocalDir(cwd string) string {
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	root, main := RepoRoot(cwd), MainRepoRoot(cwd)
	rel, err := filepath.Rel(root, cwd)
	if root == main || err != nil {
		return Dir(cwd)
	}
	return Dir(filepath.Join(main, rel))
}

// ResolvesToDir reports whether entry, read from dir, is a directory once a
// symlink is followed: os.ReadDir reports a linked directory as a link. Config
// folders are often linked in from a dotfiles or shared repo.
func ResolvesToDir(dir string, entry fs.DirEntry) bool {
	if entry.Type()&fs.ModeSymlink == 0 {
		return entry.IsDir()
	}
	info, err := os.Stat(filepath.Join(dir, entry.Name()))
	return err == nil && info.IsDir()
}

// WalkFiles calls fn for each file under root, in lexical order, following
// symlinked files and directories; a directory reached again through a link
// cycle is skipped. Unreadable entries are skipped.
func WalkFiles(root string, fn func(path string)) {
	seen := map[string]bool{}
	var walk func(dir string)
	walk = func(dir string) {
		real, err := filepath.EvalSymlinks(dir)
		if err != nil || seen[real] {
			return
		}
		seen[real] = true
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if path := filepath.Join(dir, e.Name()); ResolvesToDir(dir, e) {
				walk(path)
			} else {
				fn(path)
			}
		}
	}
	walk(root)
}
