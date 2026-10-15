package app

import (
	"path/filepath"
	"strings"

	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/setting"
)

// autoMemoryDir is the auto-memory store the main agent maintains, or "" when
// auto memory is off (setting, or SAN_DISABLE_AUTO_MEMORY).
func (m *model) autoMemoryDir() string {
	if !m.services.Setting.AutoMemory() {
		return ""
	}
	return system.AutoMemoryDir(m.env.CWD)
}

// isAutoMemoryWrite reports whether a Write/Edit call targets a markdown file
// inside the auto-memory store, which the agent maintains without prompting.
// Symlinks are resolved so a link cannot carry the write outside the store.
func isAutoMemoryWrite(dir, toolName string, args map[string]any) bool {
	if dir == "" || (toolName != "Write" && toolName != "Edit") {
		return false
	}
	path, _ := args["file_path"].(string)
	if !strings.HasSuffix(path, ".md") || !filepath.IsAbs(path) {
		return false
	}
	return setting.WithinDirectory(path, dir)
}
