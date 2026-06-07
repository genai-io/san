package identity

import (
	_ "embed"
	"os"
	"path/filepath"

	"github.com/genai-io/san/internal/confdir"
)

//go:embed README.md.tmpl
var readmeTemplate string

// EnsureUserDir creates ~/.san/identities/ and writes README.md if missing.
// Idempotent: existing README files are not overwritten.
func EnsureUserDir() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(confdir.Dir(home), "identities")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	readme := filepath.Join(dir, "README.md")
	if _, err := os.Stat(readme); err == nil {
		return nil // already exists, do not overwrite
	}
	return os.WriteFile(readme, []byte(readmeTemplate), 0o644)
}
