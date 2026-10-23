// Package confdir resolves the per-user and per-project configuration directory.
//
// confdir is a zero-dependency infrastructure leaf so every layer (including
// internal/log and internal/core) can import it without a layering violation.
package confdir

import (
	"io/fs"
	"os"
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

// MainRepoRoot is RepoRoot with a linked worktree (whose .git is a
// "gitdir: <main>/.git/worktrees/<name>" file) mapped back to the main
// checkout, so every worktree of a repository shares one key. Symlinks are
// resolved: git records the gitdir resolved, and one checkout must give one key.
func MainRepoRoot(cwd string) string {
	root := mainRepoRoot(cwd)
	if real, err := filepath.EvalSymlinks(root); err == nil {
		return real
	}
	return root
}

func mainRepoRoot(cwd string) string {
	root := RepoRoot(cwd)
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

// IsDir reports whether entry, read from dir, is a directory, following a
// symlink: os.ReadDir reports a linked directory as a link. Config folders
// are often linked in from a dotfiles or shared repo.
func IsDir(dir string, entry fs.DirEntry) bool {
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
			if path := filepath.Join(dir, e.Name()); IsDir(dir, e) {
				walk(path)
			} else {
				fn(path)
			}
		}
	}
	walk(root)
}
