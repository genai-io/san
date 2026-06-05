package identity

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/genai-io/san/internal/confdir"
)

// IsIdentityFile reports whether path points at a loadable identity markdown
// file in the user or current project identity directory.
func IsIdentityFile(cwd, path string) bool {
	if path == "" || !strings.HasSuffix(path, ".md") || strings.EqualFold(filepath.Base(path), "README.md") {
		return false
	}
	// Cheap substring guard before paying for filepath.Abs/UserHomeDir on
	// every Write/Edit tool result. Accept both the current and pre-rename dir.
	slash := filepath.ToSlash(path)
	if !strings.Contains(slash, "/"+confdir.Name+"/identities/") &&
		!strings.Contains(slash, "/"+confdir.LegacyName+"/identities/") {
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
	var roots []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		roots = append(roots, home)
	}
	if cwd != "" {
		roots = append(roots, cwd)
	}
	// Recognize identities under both the current (.san) and pre-rename (.gen)
	// config dirs so an edit to either is classified correctly.
	dirs := make([]string, 0, len(roots)*2)
	for _, root := range roots {
		dirs = append(dirs, filepath.Join(root, confdir.Name, "identities"))
		dirs = append(dirs, filepath.Join(root, confdir.LegacyName, "identities"))
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
