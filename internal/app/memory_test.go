package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsAutoMemoryWrite(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		tool string
		path string
		want bool
	}{
		{"write memory file", "Write", filepath.Join(dir, "user_role.md"), true},
		{"edit index", "Edit", filepath.Join(dir, "MEMORY.md"), true},
		{"non-markdown", "Write", filepath.Join(dir, "run.sh"), false},
		{"outside store", "Write", filepath.Join(outside, "x.md"), false},
		{"symlink escape", "Write", filepath.Join(dir, "escape", "x.md"), false},
		{"traversal", "Write", filepath.Join(dir, "..", "x.md"), false},
		{"relative path", "Write", "MEMORY.md", false},
		{"other tool", "Bash", filepath.Join(dir, "x.md"), false},
	}
	for _, c := range cases {
		if got := isAutoMemoryWrite(dir, c.tool, map[string]any{"file_path": c.path}); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	if isAutoMemoryWrite("", "Write", map[string]any{"file_path": filepath.Join(dir, "x.md")}) {
		t.Error("auto memory off must not permit anything")
	}
}
