package setting

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/genai-io/san/internal/atomicfile"
	"github.com/genai-io/san/internal/confdir"
	"github.com/genai-io/san/internal/core/system"
)

// AutoMemoryDir is cwd's auto-memory store: its repository's directory under
// autoMemoryDirectory when that is set, else system.AutoMemoryDir.
func AutoMemoryDir(cwd string) string {
	if root := autoMemoryRoot(cwd); root != "" {
		return system.AutoMemoryDirIn(root, cwd)
	}
	return system.AutoMemoryDir(cwd)
}

// IsAutoMemoryPath reports whether path is a file in some repository's
// auto-memory store, under the default root or the user's autoMemoryDirectory.
// ponytail: a root set only in a project's settings.local.json is not seen
// here (no cwd); such writes render as plain file operations.
func IsAutoMemoryPath(path string) bool {
	home, _ := os.UserHomeDir()
	if home == "" || filepath.Base(filepath.Dir(path)) != "memory" {
		return false
	}
	return system.IsAutoMemoryPath(system.AutoMemoryRoot(home), path) ||
		system.IsAutoMemoryPath(expandMemoryRoot(readMemoryRoot(filepath.Join(confdir.Dir(home), "settings.json")), home), path)
}

// autoMemoryRoot is autoMemoryDirectory from the user's own settings, or "".
// Only ~/.san/settings.json and an untracked settings.local.json count, never
// a committed project file: memory writes need no approval, so a cloned
// repository must not choose where they land.
func autoMemoryRoot(cwd string) string {
	home, _ := os.UserHomeDir()
	root := readMemoryRoot(filepath.Join(confdir.Dir(home), "settings.json"))
	// Runs on every tool call, so git is asked only about a file that sets it.
	paths := &Loader{projectDir: confdir.Dir(cwd), projectRoot: cwd}
	for _, src := range paths.projectSettingsFiles() { // later files win
		if src.local {
			if r := readMemoryRoot(src.path); r != "" && UntrackedInRepo(src.path) {
				root = r
			}
		}
	}
	return expandMemoryRoot(root, home)
}

func readMemoryRoot(path string) string {
	var s struct {
		AutoMemoryDirectory string `json:"autoMemoryDirectory"`
	}
	_ = atomicfile.ReadJSON(path, &s)
	return s.AutoMemoryDirectory
}

// expandMemoryRoot resolves a leading ~/ and returns "" for a relative path,
// a filesystem root, a NUL byte or a ~/ path that climbs out of home.
func expandMemoryRoot(root, home string) string {
	if rest, ok := strings.CutPrefix(root, "~/"); ok {
		if home == "" || !filepath.IsLocal(rest) {
			return ""
		}
		root = filepath.Join(home, rest)
	}
	root = filepath.Clean(root)
	if !filepath.IsAbs(root) || filepath.Dir(root) == root || strings.ContainsRune(root, 0) {
		return ""
	}
	return root
}
