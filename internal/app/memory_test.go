package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/setting"
)

func TestAutoMemoryStore(t *testing.T) {
	dir, user := t.TempDir(), t.TempDir()
	stores := system.MemoryStores{User: user, Project: dir}
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
		if got := autoMemoryStore(stores, c.tool, map[string]any{"file_path": c.path}) == dir; got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	if got := autoMemoryStore(stores, "Write", map[string]any{"file_path": filepath.Join(user, "x.md")}); got != user {
		t.Errorf("user store write resolved to %q", got)
	}
	if autoMemoryStore(system.MemoryStores{}, "Write", map[string]any{"file_path": filepath.Join(dir, "x.md")}) != "" {
		t.Error("auto memory off must not permit anything")
	}
}

// Memory writes skip approval in every mode, but a deny rule still stops them.
func TestPermitAutoMemoryWrite(t *testing.T) {
	dir := t.TempDir()
	stores := system.MemoryStores{Project: dir}
	args := map[string]any{"file_path": filepath.Join(dir, "a.md")}
	d := setting.NewData()
	if !permitAutoMemoryWrite(stores, "Write", args, d.DenyRule) {
		t.Error("memory write should be permitted")
	}
	d.Permissions.Deny = []string{"Write(**/a.md)"}
	if permitAutoMemoryWrite(stores, "Write", args, d.DenyRule) {
		t.Error("a deny rule must win")
	}

	// Forgetting is a plain rm of memory files; anything wider still asks.
	rm := func(cmd string) bool {
		return permitAutoMemoryWrite(stores, "Bash", map[string]any{"command": cmd}, setting.NewData().DenyRule)
	}
	a, b := filepath.Join(dir, "a.md"), filepath.Join(dir, "b.md")
	if !rm("rm "+a) || !rm("rm -f "+a+" "+b) {
		t.Error("removing memory files should be permitted")
	}
	outside := filepath.Join(t.TempDir(), "x.md")
	for _, cmd := range []string{"rm " + a + " " + outside, "rm " + filepath.Join(dir, "run.sh"), "rm -r " + dir, "rm " + a + "; rm " + outside} {
		if rm(cmd) {
			t.Errorf("%q must still ask", cmd)
		}
	}
	if permitAutoMemoryWrite(system.MemoryStores{}, "Bash", map[string]any{"command": "rm " + a}, setting.NewData().DenyRule) {
		t.Error("memory off permits no rm")
	}
}

// The turn's memory is one quiet line, grouped by verb in order; a write
// names its store.
func TestMemoryLine(t *testing.T) {
	got := ansi.Strip(memoryLine([]conv.MemoryEntry{
		{Verb: "used", Title: "Go dev", Store: "user"}, {Verb: "updated", Title: "Freeze", Store: "project"},
		{Verb: "used", Title: "Tabs", Store: "user"}, {Verb: "saved", Title: "Pnpm", Store: "user"},
	}, 100))
	if got != "◇ memory · used Go dev, Tabs · updated Freeze (project) · saved Pnpm (user)" {
		t.Errorf("memoryLine = %q", got)
	}
	if memoryLine(nil, 80) != "" {
		t.Error("a turn without memory gets no line")
	}
}
