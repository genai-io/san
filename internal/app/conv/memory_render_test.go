package conv

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/tool/toolresult"
)

func TestMemoryRows(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	mem := filepath.Join(home, ".san", "projects", "-repo", "memory")
	input := func(path, content string) string {
		b, _ := json.Marshal(map[string]string{"file_path": path, "content": content})
		return string(b)
	}
	cases := []struct {
		tool, input, want string
	}{
		{"Write", input(filepath.Join(mem, "feedback_pnpm.md"), "---\nname: use-pnpm\ndescription: use pnpm, never npm\ntype: feedback\n---\n"), "Saved memory · use pnpm, never npm"},
		{"Write", input(filepath.Join(mem, "MEMORY.md"), "- x"), "Updated memory index"},
		{"Edit", input(filepath.Join(mem, "user_role.md"), ""), "Updated memory · user_role"},
		{"Read", input(filepath.Join(mem, "user_role.md"), ""), "Recalled memory · user_role"},
	}
	for _, c := range cases {
		op, ok := parseMemoryOp(c.tool, c.input)
		if !ok {
			t.Fatalf("%s %s: not a memory op", c.tool, c.input)
		}
		if got := strings.TrimSpace(xansi.Strip(renderMemoryCall(op, 0, "●"))); got != "● "+c.want {
			t.Errorf("got %q, want %q", got, "● "+c.want)
		}
	}

	for _, path := range []string{filepath.Join(home, "notes.md"), filepath.Join(mem, "run.sh"), filepath.Join(mem, "sub", "x.md")} {
		if _, ok := parseMemoryOp("Write", input(path, "")); ok {
			t.Errorf("%s should not render as memory", path)
		}
	}

	out, _ := renderMemoryResult(ToolResultData{
		ToolName: "Write", ToolInput: cases[0].input, Nested: true,
		Details: toolresult.FileChangeDetails{AddedLines: 8, UnifiedDiff: "+secret diff"},
	})
	if got := xansi.Strip(out); !strings.Contains(got, "feedback_pnpm.md · +8 -0") || strings.Contains(got, "secret diff") {
		t.Errorf("collapsed result = %q", got)
	}
}
