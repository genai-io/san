package agent

import (
	"path/filepath"
	"strings"

	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool"
)

// autoMemoryStore identifies a markdown write within an enabled memory store.
// Symlinks are resolved so a link cannot carry the write outside the store.
func autoMemoryStore(s system.MemoryStores, toolName string, args map[string]any) string {
	if toolName != tool.ToolWrite && toolName != tool.ToolEdit {
		return ""
	}
	path, _ := args["file_path"].(string)
	if !strings.HasSuffix(path, ".md") || !filepath.IsAbs(path) {
		return ""
	}
	for _, dir := range []string{s.User, s.Project} {
		if dir != "" && setting.WithinDirectory(path, dir) {
			return dir
		}
	}
	return ""
}

// Memory maintenance is permitted even in noninteractive modes, unless denied.
func permitAutoMemoryWrite(s system.MemoryStores, name string, args map[string]any, deny func(string, map[string]any) string) bool {
	return (autoMemoryStore(s, name, args) != "" || removesOnlyMemory(s, name, args)) && deny(name, args) == ""
}

func removesOnlyMemory(s system.MemoryStores, name string, args map[string]any) bool {
	cmd, _ := args["command"].(string)
	files, ok := setting.RemovedFiles(cmd)
	if name != tool.ToolBash || !ok {
		return false
	}
	for _, f := range files {
		if autoMemoryStore(s, tool.ToolWrite, map[string]any{"file_path": f}) == "" {
			return false
		}
	}
	return true
}
