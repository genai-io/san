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
	"github.com/genai-io/sdk-go/pkg/ai"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/reminder"
)

// useMemoryStore makes a temp dir the session's project store for one test.
func useMemoryStore(t *testing.T) string {
	mem := t.TempDir()
	prev := MemoryStores
	MemoryStores = system.MemoryStores{Project: mem}
	t.Cleanup(func() { MemoryStores = prev })
	return mem
}

func TestParseMemoryOp(t *testing.T) {
	mem := useMemoryStore(t)
	input := func(path string) string {
		b, _ := json.Marshal(map[string]string{"file_path": path})
		return string(b)
	}
	cases := []struct {
		tool, input, verb string
		index             bool
	}{
		{"Write", input(filepath.Join(mem, "feedback_pnpm.md")), "Saved", false},
		{"Write", input(filepath.Join(mem, "MEMORY.md")), "Saved", true},
		{"Edit", input(filepath.Join(mem, "user_role.md")), "Updated", false},
		{"Read", input(filepath.Join(mem, "user_role.md")), "Read", false},
		{"Bash", `{"command":"rm -f ` + filepath.Join(mem, "a.md") + " " + filepath.Join(mem, "b.md") + `"}`, "Deleted", false},
	}
	for _, c := range cases {
		op, ok := parseMemoryOp(c.tool, c.input)
		if !ok || op.verb != c.verb || op.index != c.index {
			t.Errorf("%s %s = %+v, %v", c.tool, c.input, op, ok)
		}
	}
	for _, path := range []string{filepath.Join(filepath.Dir(mem), "notes.md"), filepath.Join(mem, "run.sh"), filepath.Join(mem, "sub", "x.md")} {
		if _, ok := parseMemoryOp("Write", input(path)); ok {
			t.Errorf("%s should not count as memory", path)
		}
	}
}

// Memory calls leave the chat flow — a failed one still shows itself — and the
// turn's dealings with memory come back as one ordered list: recalled, used,
// saved or updated (a write outranks a read), and forgotten when a memory it
// touched is gone by the turn's end.
func TestTurnMemory(t *testing.T) {
	mem := useMemoryStore(t)
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
	tabs := write("tabs.md", "---\nname: tabs\ndescription: tabs over spaces\n---\n")
	gone := filepath.Join(mem, "old.md")              // read, then deleted this turn
	tabsGone := filepath.Join(mem, "spaces.md")       // removed by rm this turn, never read
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
	if !strings.Contains(out, "Read") {
		t.Errorf("a failed memory call should show as itself: %q", out)
	}

	recall := write("recall.md", "---\nname: recall\ndescription: attached memory\n---\n")
	attached := "file body\n\n" + reminder.WrapWithSource("Memories relevant to this message:\n\nMemory `"+recall+"` (project store):\nbody", reminder.ProviderMemoryRecall)
	conv := ConversationModel{Messages: []core.ChatMessage{
		{Role: core.ChatUser, Content: "earlier turn"},
		{Role: core.ChatAssistant, ToolCalls: []core.ToolCall{call("x", "Read", freeze, nil)}},
		{Role: core.ChatUser, Content: "remember"},
		{Role: core.ChatAssistant, ToolCalls: append(reads, call("r3", "Read", tabs, nil), call("r4", "Read", gone, nil))},
		{Role: core.ChatUser, ToolResult: &core.ToolResult{ToolCallID: "r3", ToolName: "Read", Content: ai.TextContent(attached)}},
		{Role: core.ChatAssistant, ToolCalls: []core.ToolCall{
			call("w1", "Write", terse, nil),
			call("e1", "Edit", terse, nil),
			call("e2", "Edit", freeze, map[string]string{"old_string": "description: freeze until 10/30", "new_string": "description: freeze until 11/5"}),
			{ID: "d1", Name: "Bash", Input: `{"command":"rm ` + tabsGone + `"}`},
			call("e3", "Edit", index, map[string]string{"old_string": "- [Old note](old.md) — x\n- [Freeze](freeze.md) — y", "new_string": "- [Freeze](freeze.md) — y"}),
		}},
	}}
	var got []string
	for _, e := range conv.TurnMemory("") {
		got = append(got, e.Verb+":"+e.Title)
	}
	want := []string{"saved:terse answers", "used:tabs over spaces", "forgot:Old note", "recalled:attached memory", "updated:Freeze", "forgot:spaces"}
	if !slices.Equal(got, want) {
		t.Errorf("TurnMemory = %q, want %q", got, want)
	}
	if shown := xansi.Strip(RenderToolResultInline(ToolResultData{ToolName: "Bash", Content: attached, Nested: true, Expanded: true}, nil)); strings.Contains(shown, "Memories relevant") {
		t.Errorf("a recall reminder showed in its tool result: %q", shown)
	}
}

// A step that only keeps memory draws nothing, its thinking included — only a
// spinner row while it is the live tail; one that also answers, or whose
// memory call failed, draws as usual.
func TestMemoryStepDrawsNothing(t *testing.T) {
	mem := useMemoryStore(t)
	read := core.ToolCall{ID: "r1", Name: "Read", Input: `{"file_path":"` + filepath.Join(mem, "MEMORY.md") + `"}`}
	render := func(step core.ChatMessage, failed bool) string {
		msgs := []core.ChatMessage{
			{Role: core.ChatUser, Content: "remember tabs"},
			step,
			{Role: core.ChatUser, ToolResult: &core.ToolResult{ToolCallID: "r1", ToolName: "Read", IsError: failed}},
		}
		p := RenderContext{Width: 80, Messages: msgs, InlinedResults: PrecomputeInlinedResults(msgs, 0)}
		return xansi.Strip(RenderMessageAt(p, 1, false))
	}
	quiet := core.ChatMessage{Role: core.ChatAssistant, Thinking: "**Checking existing memories**", ToolCalls: []core.ToolCall{read}}
	if got := render(quiet, false); got != "" {
		t.Errorf("memory-only step drew %q", got)
	}
	live := []core.ChatMessage{{Role: core.ChatUser, Content: "remember tabs"}, quiet}
	p := RenderContext{Width: 80, StreamActive: true, SpinnerView: "*", Messages: live, InlinedResults: PrecomputeInlinedResults(live, 0)}
	if got := RenderMessageAt(p, 1, true); got != "" {
		t.Errorf("committed live memory step = %q, want nothing in scrollback", got)
	}
	p.Interactive = true
	if got := strings.TrimSpace(xansi.Strip(RenderMessageAt(p, 1, true))); got != "* Recalling memory..." {
		t.Errorf("live memory step = %q, want its spinner row", got)
	}
	if got := render(quiet, true); !strings.Contains(got, "Read(") {
		t.Errorf("a failed memory step should show, got %q", got)
	}
	answer := quiet
	answer.Content = "Noted."
	if got := render(answer, false); !strings.Contains(got, "Noted.") {
		t.Errorf("a step with text should draw, got %q", got)
	}
}

// A message that enters mid-turn does not cut the turn short.
func TestTurnMemoryAcrossMidTurnMessage(t *testing.T) {
	mem := useMemoryStore(t)
	gone := filepath.Join(mem, "freeze.md")
	conv := ConversationModel{Messages: []core.ChatMessage{
		{ID: "u1", Role: core.ChatUser, Content: "forget the freeze"},
		{Role: core.ChatAssistant, ToolCalls: []core.ToolCall{{ID: "d1", Name: "Bash", Input: `{"command":"rm ` + gone + `"}`}}},
		{ID: "u2", Role: core.ChatUser, Content: "and say ok"},
		{Role: core.ChatAssistant, Content: "ok"},
	}}
	if got := conv.TurnMemory("u1"); len(got) != 1 || got[0].Verb != "forgot" {
		t.Errorf("TurnMemory(u1) = %+v, want the forget before u2", got)
	}
}
