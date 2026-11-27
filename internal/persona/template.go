package persona

import (
	_ "embed"
	"os"
	"path/filepath"
)

//go:embed README.md.tmpl
var readmeTemplate string

// UserDir returns the user-level personas root (~/.san/personas).
func UserDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return personasDir(home), nil
}

// EnsureUserDir creates ~/.san/personas/ and writes README.md if missing.
// Idempotent: an existing README is not overwritten.
func EnsureUserDir() error {
	dir, err := UserDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	readme := filepath.Join(dir, "README.md")
	if _, err := os.Stat(readme); err == nil {
		return nil // already exists, do not overwrite
	}
	return os.WriteFile(readme, []byte(readmeTemplate), 0o644)
}
