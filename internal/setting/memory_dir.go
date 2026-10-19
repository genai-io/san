package setting

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/genai-io/san/internal/atomicfile"
	"github.com/genai-io/san/internal/confdir"
	"github.com/genai-io/san/internal/core/system"
)

// MemoryStoresFor is cwd's auto-memory stores. autoMemoryDirectory moves the
// store of the file that sets it: ~/.san/settings.json the user store, an
// untracked settings.local.json the project store. A committed project file
// never counts: memory writes need no approval, so a cloned repository must
// not choose where they land.
func MemoryStoresFor(cwd string) system.MemoryStores {
	home, _ := os.UserHomeDir()
	s := system.MemoryStores{User: system.UserMemoryDir(home), Project: system.AutoMemoryDir(cwd)}
	if d := expandMemoryDir(readMemoryDir(filepath.Join(confdir.Dir(home), "settings.json")), home); d != "" {
		s.User = d
	}
	// Runs on every tool call, so git is asked only about a file that sets it.
	paths := &Loader{projectDir: confdir.Dir(cwd), projectRoot: cwd}
	for _, src := range paths.projectSettingsFiles() { // later files win
		if src.local {
			if d := expandMemoryDir(readMemoryDir(src.path), home); d != "" && UntrackedInRepo(src.path) {
				s.Project = d
			}
		}
	}
	return s
}

// IsAutoMemoryPath reports whether path is a markdown file in a default store
// or in one of the working directory's stores, so the chat can show it as
// memory.
func IsAutoMemoryPath(path string) bool {
	if system.IsAutoMemoryPath(path) {
		return true
	}
	if !strings.HasSuffix(path, ".md") {
		return false
	}
	wd, err := os.Getwd()
	if err != nil {
		return false
	}
	s := MemoryStoresFor(wd)
	dir := filepath.Dir(filepath.Clean(path))
	return dir == s.User || dir == s.Project
}

func readMemoryDir(path string) string {
	var s struct {
		AutoMemoryDirectory string `json:"autoMemoryDirectory"`
	}
	_ = atomicfile.ReadJSON(path, &s)
	return s.AutoMemoryDirectory
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
