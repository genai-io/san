package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
		parent := strings.TrimRight(dir, "/\\")
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
