package identity

import (
	"os"
	"path/filepath"
	"strings"
)

// IsIdentityFile reports whether path points at a loadable identity markdown
// file in the user or current project identity directory.
func IsIdentityFile(cwd, path string) bool {
	if path == "" || !strings.HasSuffix(path, ".md") || strings.EqualFold(filepath.Base(path), "README.md") {
		return false
	}
	// Cheap substring guard before paying for filepath.Abs/UserHomeDir on
	// every Write/Edit tool result.
	if !strings.Contains(filepath.ToSlash(path), "/.gen/identities/") {
		return false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	for _, dir := range identityDirs(cwd) {
		if isWithinDir(abs, dir) {
			return true
		}
	}
	return false
}

func identityDirs(cwd string) []string {
	dirs := make([]string, 0, 2)
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dirs = append(dirs, filepath.Join(home, ".gen", "identities"))
	}
	if cwd != "" {
		dirs = append(dirs, filepath.Join(cwd, ".gen", "identities"))
	}
	return dirs
}

func isWithinDir(path, dir string) bool {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absDir, path)
	if err != nil || rel == "." || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
