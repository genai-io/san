package conv

import (
	"encoding/json"
	"os"
	"path/filepath"
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

// Memory calls leave the main flow: one thinking-style row stands in for them,
// a failed call still shows itself, and the turn's writes fold into one change
// per file for the recap card.
func TestMemoryCallsFold(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	mem := filepath.Join(home, ".san", "memory")
	topic := filepath.Join(mem, "terse.md")
	call := func(id, name, path string) core.ToolCall {
		b, _ := json.Marshal(map[string]string{"file_path": path})
		return core.ToolCall{ID: id, Name: name, Input: string(b)}
	}
	calls := []core.ToolCall{call("r1", "Read", topic), call("r2", "Read", filepath.Join(mem, "MEMORY.md"))}
	out := xansi.Strip(RenderToolCalls(ToolCallsParams{
		ToolCalls: calls,
		ResultMap: map[string]ToolResultData{"r1": {ToolName: "Read"}, "r2": {ToolName: "Read"}},
	}))
	if strings.TrimSpace(out) != "✦ Read 1 memory · terse" {
		t.Errorf("folded reads = %q", out)
	}
	joined, ok := joinThinkingLine(ThinkingStyle.Render("✦ Checking the rules")+"\n", "Read 1 memory · terse", 80)
	if got := xansi.Strip(joined); !ok || got != "✦ Checking the rules · Read 1 memory · terse\n" {
		t.Errorf("joined thinking line = %q (ok=%v)", got, ok)
	}
	if _, ok := joinThinkingLine("", "Read 1 memory", 80); ok {
		t.Error("no thinking tail: the summary keeps its own row")
	}
	out = xansi.Strip(RenderToolCalls(ToolCallsParams{
		ToolCalls: calls[:1],
		ResultMap: map[string]ToolResultData{"r1": {ToolName: "Read", IsError: true, Content: "boom"}},
	}))
	if !strings.Contains(out, "memory") || strings.Contains(out, "✦") {
		t.Errorf("a failed memory call should show as itself: %q", out)
	}

	if err := os.MkdirAll(mem, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(topic, []byte("---\nname: terse\ndescription: terse answers\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	conv := ConversationModel{Messages: []core.ChatMessage{
		{Role: core.ChatUser, Content: "remember"},
		{Role: core.ChatAssistant, ToolCalls: []core.ToolCall{call("w1", "Write", topic)}},
		{Role: core.ChatAssistant, ToolCalls: []core.ToolCall{call("e1", "Edit", topic), call("e2", "Edit", filepath.Join(mem, "MEMORY.md"))}},
	}}
	got := conv.TurnMemoryChanges()
	if len(got) != 1 || got[0].Verb != "saved" || got[0].Description != "terse answers" {
		t.Errorf("turn changes = %+v", got)
	}
}
