package task

import (
	"path/filepath"

	"github.com/genai-io/san/internal/infra/confdir"
)

// SessionDir holds a session's tracker items and task outputs.
func SessionDir(homeDir, id string) string {
	return filepath.Join(confdir.Dir(homeDir), "tasks", id)
}
