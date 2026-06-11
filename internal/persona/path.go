package persona

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/genai-io/san/internal/confdir"
)

// IsPersonaFile reports whether path points inside a user- or project-level
// persona directory (any file under <root>/.san/personas/<name>/). Used to
// trigger a registry reload when a persona file is edited.
func IsPersonaFile(cwd, path string) bool {
	if path == "" {
		return false
	}
	// Cheap substring guard before paying for filepath.Abs/UserHomeDir.
	slash := filepath.ToSlash(path)
	if !strings.Contains(slash, "/"+confdir.Name+"/personas/") {
		return false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	for _, dir := range personaRoots(cwd) {
		if isWithinDir(abs, dir) {
			return true
		}
	}
	return false
}

func personaRoots(cwd string) []string {
	var roots []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		roots = append(roots, home)
	}
	if cwd != "" {
		roots = append(roots, cwd)
	}
	dirs := make([]string, 0, len(roots))
	for _, root := range roots {
		dirs = append(dirs, filepath.Join(root, confdir.Name, "personas"))
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
