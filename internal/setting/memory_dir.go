package setting

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/genai-io/san/internal/atomicfile"
	"github.com/genai-io/san/internal/confdir"
	"github.com/genai-io/san/internal/core/system"
)

// AutoMemoryDir is cwd's auto-memory store: autoMemoryDirectory from the
// user's own settings when set, else system.AutoMemoryDir. Only
// ~/.san/settings.json and an untracked settings.local.json count, never a
// committed project file: memory writes need no approval, so a cloned
// repository must not choose where they land.
func AutoMemoryDir(cwd string) string {
	read := func(path string) string {
		var s struct {
			AutoMemoryDirectory string `json:"autoMemoryDirectory"`
		}
		_ = atomicfile.ReadJSON(path, &s)
		return s.AutoMemoryDirectory
	}
	home, _ := os.UserHomeDir()
	dir := read(filepath.Join(confdir.Dir(home), "settings.json"))
	// Runs on every tool call, so git is asked only about a file that sets it.
	paths := &Loader{projectDir: confdir.Dir(cwd), projectRoot: cwd}
	for _, src := range paths.projectSettingsFiles() { // later files win
		if src.local {
			if d := read(src.path); d != "" && UntrackedInRepo(src.path) {
				dir = d
			}
		}
	}
	if rest, ok := strings.CutPrefix(dir, "~/"); ok && home != "" {
		dir = filepath.Join(home, rest)
	}
	if !filepath.IsAbs(dir) {
		return system.AutoMemoryDir(cwd)
	}
	return filepath.Clean(dir)
}
