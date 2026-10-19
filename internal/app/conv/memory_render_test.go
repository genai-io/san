package conv

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/core"
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
		{"Read", input(filepath.Join(mem, "user_role.md"), ""), "Read memory · user_role"},
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

// Memory calls leave the chat flow — a failed one still shows itself — and the
// turn's dealings with memory come back as one ordered list: used, saved or
// updated once per file.
func TestTurnMemory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	mem := filepath.Join(home, ".san", "memory")
	index := filepath.Join(mem, "MEMORY.md")
	call := func(id, name, path string, extra map[string]string) core.ToolCall {
		args := map[string]string{"file_path": path}
		maps.Copy(args, extra)
		b, _ := json.Marshal(args)
		return core.ToolCall{ID: id, Name: name, Input: string(b)}
	}
	write := func(name, body string) string {
		p := filepath.Join(mem, name)
		if err := os.MkdirAll(mem, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	terse := write("terse.md", "---\nname: terse\ndescription: terse answers\n---\n")
	freeze := write("freeze.md", "---\nname: freeze\ndescription: freeze until 11/5\n---\n")
	write("MEMORY.md", "- [Freeze](freeze.md) — y\n") // the index as the turn left it

	reads := []core.ToolCall{call("r1", "Read", terse, nil), call("r2", "Read", index, nil)}
	out := xansi.Strip(RenderToolCalls(ToolCallsParams{
		ToolCalls: reads,
		ResultMap: map[string]ToolResultData{"r1": {ToolName: "Read"}, "r2": {ToolName: "Read"}},
	}))
	if strings.TrimSpace(out) != "" {
		t.Errorf("memory calls should leave the chat flow, got %q", out)
	}
	out = xansi.Strip(RenderToolCalls(ToolCallsParams{
		ToolCalls: reads[:1],
		ResultMap: map[string]ToolResultData{"r1": {ToolName: "Read", IsError: true, Content: "boom"}},
	}))
	if !strings.Contains(out, "memory") {
		t.Errorf("a failed memory call should show as itself: %q", out)
	}

	conv := ConversationModel{Messages: []core.ChatMessage{
		{Role: core.ChatUser, Content: "earlier turn"},
		{Role: core.ChatAssistant, ToolCalls: []core.ToolCall{call("x", "Read", freeze, nil)}},
		{Role: core.ChatUser, Content: "remember"},
		{Role: core.ChatAssistant, ToolCalls: reads},
		{Role: core.ChatAssistant, ToolCalls: []core.ToolCall{
			call("w1", "Write", terse, nil),
			call("e1", "Edit", terse, nil),
			call("e2", "Edit", freeze, map[string]string{"old_string": "description: freeze until 10/30", "new_string": "description: freeze until 11/5"}),
		}},
	}}
	var got []string
	for _, e := range conv.TurnMemory() {
		got = append(got, e.Verb+":"+e.Title)
	}
	want := []string{"used:terse answers", "saved:terse answers", "updated:Freeze"}
	if !slices.Equal(got, want) {
		t.Errorf("TurnMemory = %q, want %q", got, want)
	}
}
