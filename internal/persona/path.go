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
	return withinAny(path, scopeDirs(cwd, "personas"))
}

// scopeDirs returns the user- and project-level <sub> directories.
func scopeDirs(cwd, sub string) []string {
	var out []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		out = append(out, filepath.Join(home, confdir.Name, sub))
	}
	if cwd != "" {
		out = append(out, filepath.Join(cwd, confdir.Name, sub))
	}
	return out
}

func withinAny(path string, roots []string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	for _, dir := range roots {
		if isWithinDir(abs, dir) {
			return true
		}
	}
	return false
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
