package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/genai-io/san/internal/confdir"
)

// ResolvePath follows symlinks even when the file or its parents are new.
func ResolvePath(path string) (string, error) {
	// Abs/Dir clean '..' before symlinks, changing which file the OS would open.
	if !filepath.IsAbs(path) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		path = cwd + string(os.PathSeparator) + path
	}
	candidate, suffix, links := path, "", 0
	for {
		resolved, err := filepath.EvalSymlinks(candidate)
		if err == nil {
			return filepath.Join(resolved, suffix), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		dir, base := filepath.Split(candidate)
		if info, statErr := os.Lstat(candidate); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			links++
			if links > 40 {
				return "", fmt.Errorf("too many symlinks in %s", path)
			}
			target, err := os.Readlink(candidate)
			if err != nil {
				return "", err
			}
			if !filepath.IsAbs(target) {
				target = dir + target
			}
			candidate = target
			continue
		}
		parent := dir
		for len(parent) > 0 && os.IsPathSeparator(parent[len(parent)-1]) {
			parent = parent[:len(parent)-1]
		}
		if len(parent) <= len(filepath.VolumeName(dir)) {
			parent = dir
		}
		if parent == "" || parent == candidate {
			return "", err
		}
		suffix = filepath.Join(base, suffix)
		candidate = parent
	}
}

// writeTarget is where Write puts path's contents. A symlink inside the user's
// own config folder (~/.san) is written through, so a file linked in from a
// dotfiles repo stays linked. Anywhere else, a project's .san included, the
// link came with the folder and is replaced: a write never leaves it.
func writeTarget(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if rel, err := filepath.Rel(confdir.Dir(home), abs); err != nil || !filepath.IsLocal(rel) {
		return path
	}
	if resolved, err := ResolvePath(abs); err == nil {
		return resolved
	}
	return path
}
