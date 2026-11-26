package setting

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/infra/atomicfile"
	"github.com/genai-io/san/internal/infra/confdir"
)

// MemoryStoreFor resolves the repository's one Auto Memory directory.
// Only the main checkout's untracked settings.local.json can redirect it.
// Neither a cloned settings file nor the former user-store setting counts.
func MemoryStoreFor(cwd string) system.MemoryStore {
	home, _ := os.UserHomeDir()
	path := memoryDirectorySettings(cwd)
	if dir := expandMemoryDir(readMemoryDir(path), home); dir != "" && UntrackedInRepo(path) {
		return system.MemoryStore(dir)
	}
	return system.MemoryStore(system.AutoMemoryDir(cwd))
}

func memoryDirectorySettings(cwd string) string {
	return filepath.Join(confdir.Dir(confdir.MainRepoRoot(cwd)), "settings.local.json")
}

func readMemoryDir(path string) string {
	var d Data
	_ = atomicfile.ReadJSON(path, &d)
	return d.AutoMemoryDirectory
}

// expandMemoryDir resolves a leading ~/ and returns "" for a relative path,
// a filesystem root, a NUL byte or a ~/ path that climbs out of home.
func expandMemoryDir(dir, home string) string {
	if rest, ok := strings.CutPrefix(dir, "~/"); ok {
		if home == "" || !filepath.IsLocal(rest) {
			return ""
		}
		dir = filepath.Join(home, rest)
	}
	dir = filepath.Clean(dir)
	if !filepath.IsAbs(dir) || filepath.Dir(dir) == dir || strings.ContainsRune(dir, 0) {
		return ""
	}
	return dir
}

// SaveMemoryDirectory sets or clears a repository-wide location override in
// the main checkout's untracked settings.local.json.
func SaveMemoryDirectory(cwd, dir string) error {
	home, _ := os.UserHomeDir()
	if dir != "" && expandMemoryDir(dir, home) == "" {
		return fmt.Errorf("%q must be an absolute path or start with ~/", dir)
	}
	path := memoryDirectorySettings(cwd)
	if !UntrackedInRepo(path) {
		return fmt.Errorf("settings.local.json is tracked by git or outside a repository, so it cannot move this repository's memory")
	}
	return atomicfile.WithWriteLock(path, func() error {
		doc := map[string]any{}
		if err := atomicfile.ReadJSON(path, &doc); err != nil {
			return err
		}
		if doc == nil {
			doc = map[string]any{}
		}
		if dir == "" {
			delete(doc, "autoMemoryDirectory")
		} else {
			doc["autoMemoryDirectory"] = dir
		}
		return atomicfile.WriteJSON(path, doc, 0o644)
	})
}
