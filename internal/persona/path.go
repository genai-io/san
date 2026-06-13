package persona

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/genai-io/san/internal/confdir"
)

// IsPersonaFile reports whether path points at a file that affects the persona
// registry: anything under a user/project personas/ or identities/ directory
// (personas absorb the legacy single-file identities). Used to trigger a
// registry reload when such a file is edited.
func IsPersonaFile(cwd, path string) bool {
	if path == "" {
		return false
	}
	// Cheap substring guard before paying for filepath.Abs/UserHomeDir.
	slash := filepath.ToSlash(path)
	for _, sub := range []string{"personas", "identities"} {
		if strings.Contains(slash, "/"+confdir.Name+"/"+sub+"/") && withinAny(path, scopeDirs(cwd, sub)) {
			return true
		}
	}
	return false
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
