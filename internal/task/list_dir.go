package task

import (
	"path/filepath"

	"github.com/genai-io/san/internal/infra/confdir"
)

// ListDir holds tracker items and task outputs for a task list.
func ListDir(homeDir, listID string) string {
	return filepath.Join(confdir.Dir(homeDir), "tasks", listID)
}
