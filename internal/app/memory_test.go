package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"

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

func TestAfterMemoryWrite(t *testing.T) {
	dir := t.TempDir()
	topic := filepath.Join(dir, "user_role.md")
	index := filepath.Join(dir, "MEMORY.md")
	if err := os.WriteFile(topic, []byte("---\nname: user-role\ntype: user\n---\n\nGo dev.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(index, []byte("- [Gone](gone.md) — x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	call := func(path string) sdkagent.PostToolContext {
		return sdkagent.PostToolContext{
			Call:   ai.ToolCall{Name: "Write", Input: `{"file_path":"` + path + `"}`},
			Result: sdkagent.Result{Content: ai.TextContent("ok")},
		}
	}

	stores := system.MemoryStores{Project: dir}
	r := afterMemoryWrite(stores, call(topic))
	if r == nil || !strings.Contains(r.Content.Text(), "modified") {
		t.Fatalf("topic write: %+v", r)
	}
	if got, _ := os.ReadFile(topic); !strings.Contains(string(got), "\nmodified: ") {
		t.Errorf("topic not stamped:\n%s", got)
	}

	r = afterMemoryWrite(stores, call(index))
	if r == nil || !strings.Contains(r.Content.Text(), "gone.md") || !strings.Contains(r.Content.Text(), "user_role.md") {
		t.Fatalf("index write should report the dangling link and the orphan: %+v", r)
	}

	failed := call(topic)
	failed.Err = errors.New("boom")
	if afterMemoryWrite(stores, failed) != nil || afterMemoryWrite(system.MemoryStores{}, call(topic)) != nil {
		t.Error("failed writes and memory off must pass through")
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
}

// The turn's memory reads as one line for one verb and an aligned block for
// several, writes naming their store.
func TestMemoryLine(t *testing.T) {
	s := system.MemoryStores{User: "/u", Project: "/p"}
	one := ansi.Strip(memoryLine(s, []conv.MemoryEntry{{Verb: "used", Path: "/u/a.md", Title: "Go dev"}, {Verb: "used", Path: "/p/b.md", Title: "Freeze"}}, 80))
	if one != "◇ memory  used  Go dev, Freeze" {
		t.Errorf("one verb = %q", one)
	}
	block := ansi.Strip(memoryLine(s, []conv.MemoryEntry{
		{Verb: "used", Path: "/u/a.md", Title: "Go dev"},
		{Verb: "updated", Path: "/p/b.md", Title: "Freeze", Change: "10/30 → 11/5"},
	}, 80))
	want := "◇ memory  used     Go dev\n          updated  Freeze · 10/30 → 11/5 · project"
	if block != want {
		t.Errorf("block =\n%s\nwant\n%s", block, want)
	}
	if memoryLine(s, nil, 80) != "" {
		t.Error("a turn without memory gets no line")
	}
}
