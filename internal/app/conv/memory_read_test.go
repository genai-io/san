package conv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/reminder"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func memoryReadFixture(t *testing.T) (string, core.ToolCall) {
	t.Helper()
	mem := useMemoryStore(t)
	path := filepath.Join(mem, "sign-off.md")
	if err := os.WriteFile(path, []byte("---\ndescription: Sign off every PR commit\n---\nPrivate memory body"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mem, "MEMORY.md"), []byte("- [Sign off PR commits](sign-off.md) — sign commits\n"), 0600); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]string{"file_path": path})
	return path, core.ToolCall{ID: "memory", Name: "Read", Input: string(input)}
}

func TestMemoryReadNoticeStaysWithItsToolStep(t *testing.T) {
	path, read := memoryReadFixture(t)
	before := core.ToolCall{ID: "before", Name: "Bash", Input: `{"command":"before"}`}
	after := core.ToolCall{ID: "after", Name: "Bash", Input: `{"command":"after"}`}
	params := ToolCallsParams{
		ToolCalls: []core.ToolCall{before, read, after}, Width: 80,
		ResultMap: map[string]ToolResultData{
			before.ID: {ToolName: "Bash", Content: "before output"},
			read.ID:   {ToolName: "Read", Content: "Private memory body"},
			after.ID:  {ToolName: "Bash", Content: "after output"},
		},
	}
	out := stripANSI(RenderToolCalls(params))
	last := -1
	for _, text := range []string{"Bash(before)", "⟡ Memory · read: Sign off PR commits", "Bash(after)"} {
		i := strings.Index(out, text)
		if i <= last || strings.Count(out, text) != 1 {
			t.Fatalf("memory read must stay in its original tool slot (%q): %q", text, out)
		}
		last = i
	}
	if strings.Contains(out, path) || strings.Contains(out, "Private memory body") || strings.Contains(out, "used ") {
		t.Fatalf("compact memory notice leaked its file operation or old wording: %q", out)
	}

	params.ToolCallsExpanded = true
	params.Width = 300
	result := params.ResultMap[read.ID]
	result.Expanded = true
	params.ResultMap[read.ID] = result
	if expanded := stripANSI(RenderToolCalls(params)); !strings.Contains(expanded, path) || !strings.Contains(expanded, "Private memory body") {
		t.Fatalf("expansion must still expose the actual read: %q", expanded)
	}
	params.ToolCallsExpanded = false
	params.ResultMap[read.ID] = ToolResultData{ToolName: "Read", IsError: true, Content: "file not found"}
	failed := stripANSI(RenderToolCalls(params))
	if !strings.Contains(failed, "file not found") || strings.Contains(failed, "Memory · read:") {
		t.Fatalf("a failed read must not claim the memory was read: %q", failed)
	}
}

func TestMemoryReadOnlyStepAppearsBeforeTheAnswer(t *testing.T) {
	_, read := memoryReadFixture(t)
	msgs := []core.ChatMessage{
		{Role: core.ChatUser, Content: "submit the PR"},
		{Role: core.ChatAssistant, Thinking: "Checking the commit convention", ToolCalls: []core.ToolCall{read}},
		{Role: core.ChatUser, ToolResult: &core.ToolResult{ToolCallID: read.ID, ToolName: "Read", Content: ai.TextContent("Private memory body")}},
		{Role: core.ChatAssistant, Content: "The PR is ready."},
	}
	p := RenderContext{Width: 80, Messages: msgs, InlinedResults: PrecomputeInlinedResults(msgs, 0)}
	out := stripANSI(RenderMessageRange(p, 0, len(msgs), false))
	thinking, notice, answer := strings.Index(out, "Checking the commit convention"), strings.Index(out, "⟡ Memory · read: Sign off PR commits"), strings.Index(out, "The PR is ready.")
	if thinking < 0 || notice <= thinking || answer <= notice || strings.Count(out, "Sign off PR commits") != 1 {
		t.Fatalf("memory-only steps must keep their notice before the answer: %q", out)
	}
}

func TestMemoryReadMarkerBreathesOnlyWhileReading(t *testing.T) {
	_, read := memoryReadFixture(t)
	p := ToolCallsParams{ToolCalls: []core.ToolCall{read}, PendingCalls: []core.ToolCall{read}, Width: 80}
	render := func(frame int) string {
		p.Frame, p.SpinnerView = frame, kit.LiveDot(frame)
		return RenderToolCalls(p)
	}
	faint, peak := render(0), render(3)
	if faint == peak || stripANSI(faint) != stripANSI(peak) || !strings.Contains(stripANSI(faint), "⟡ Memory · reading: Sign off PR commits") {
		t.Fatalf("the memory marker should breathe without flashing its text: %q / %q", faint, peak)
	}
	p.ResultMap = map[string]ToolResultData{read.ID: {ToolName: "Read", Content: "Private memory body"}}
	if a, b := render(0), render(3); a != b || !strings.Contains(stripANSI(a), "Memory · read:") {
		t.Fatalf("a completed read should keep a static memory marker: %q / %q", a, b)
	}
}

func TestRecalledMemoryAppearsBesideTheReceivingToolResult(t *testing.T) {
	path, _ := memoryReadFixture(t)
	attached := "source body\n\n" + reminder.WrapWithSource("Memories relevant to this message:\n\nMemory `"+path+"` (project store, saved 2026-10-24):\nPrivate recalled body", reminder.ProviderMemoryRecall)
	for _, call := range []core.ToolCall{
		{ID: "normal", Name: "Read", Input: `{"file_path":"main.go"}`},
		{ID: "index", Name: "Read"},
	} {
		if call.ID == "index" {
			input, _ := json.Marshal(map[string]string{"file_path": filepath.Join(filepath.Dir(path), "MEMORY.md")})
			call.Input = string(input)
		}
		m := NewModel(80)
		m.Messages = []core.ChatMessage{
			{Role: core.ChatAssistant, ToolCalls: []core.ToolCall{call}},
			{Role: core.ChatUser, ToolResult: &core.ToolResult{ToolCallID: call.ID, ToolName: "Read", Content: ai.TextContent("source body")}},
		}
		// The SDK first draws the raw result at ToolEnd. Its later MessageAdded
		// carries the filtered, model-facing result containing the recall.
		applyAgentEvent(nil, &m, sdkagent.MessageAdded{Message: core.ToolResultMessage(core.ToolResult{
			ToolCallID: call.ID, ToolName: "Read", Content: ai.TextContent(attached),
		})})
		m.Append(core.ChatMessage{Role: core.ChatAssistant, Content: "Answer after recall"})
		p := RenderContext{Width: 80, Messages: m.Messages, InlinedResults: PrecomputeInlinedResults(m.Messages, 0)}
		out := stripANSI(RenderMessageRange(p, 0, len(m.Messages), false))
		notice := strings.Index(out, "⟡ Memory · recalled: Sign off PR commits")
		if notice < 0 || notice >= strings.Index(out, "Answer after recall") || strings.Count(out, "Sign off PR commits") != 1 {
			t.Fatalf("%s: recall notice must belong to its receiving result: %q", call.ID, out)
		}
		if strings.Contains(out, "Private recalled body") || strings.Contains(out, "Memories relevant") {
			t.Fatalf("%s: recall notice leaked reminder contents: %q", call.ID, out)
		}
	}
}

func TestMemoryRecallNoticeRetainsItsTitleAfterResume(t *testing.T) {
	path, _ := memoryReadFixture(t)
	attached := "source body\n\n" + reminder.WrapWithSource("Memories relevant to this message:\n\nMemory `"+path+"` (project store):\nPrivate recalled body", reminder.ProviderMemoryRecall)
	notice := recalledMemoryNotice(attached, "result")
	if notice == "" {
		t.Fatal("a delivered recall must produce a persistable notice")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(filepath.Dir(path), "MEMORY.md")); err != nil {
		t.Fatal(err)
	}
	// Resumed messages carry the filtered native result plus the separate
	// display notice. The reminder must not generate a second notice or need
	// the original file to keep the title captured when it was delivered.
	call := core.ToolCall{ID: "read", Name: "Read", Input: `{"file_path":"main.go"}`}
	msgs := []core.ChatMessage{
		{Role: core.ChatAssistant, ToolCalls: []core.ToolCall{call}},
		{Role: core.ChatUser, ToolResult: &core.ToolResult{ToolCallID: call.ID, ToolName: "Read", Content: ai.TextContent(attached)}},
		{Role: core.ChatNotice, Content: notice},
		{Role: core.ChatAssistant, Content: "Answer after recall"},
	}
	p := RenderContext{Width: 28, Messages: msgs, InlinedResults: PrecomputeInlinedResults(msgs, 0)}
	out := stripANSI(RenderMessageRange(p, 0, len(msgs), false))
	compact := strings.Join(strings.Fields(out), "")
	if strings.Count(compact, "SignoffPRcommits") != 1 || strings.Contains(out, "Private recalled body") {
		t.Fatalf("resume lost or duplicated the captured recall title: %q", out)
	}
	for line := range strings.SplitSeq(out, "\n") {
		if lipgloss.Width(line) > p.Width {
			t.Fatalf("resumed notice exceeds terminal width: %q", line)
		}
	}
}

func TestMemoryRecallNoticeIgnoresOrdinaryToolOutput(t *testing.T) {
	path, _ := memoryReadFixture(t)
	heading := "Memory `" + path + "` (project store):\nPrivate memory body"
	for _, content := range []string{
		heading,
		reminder.WrapWithSource(heading, reminder.ProviderMemoryAuto),
		"<system-reminder source=\"memory-recall\">\n" + heading,
	} {
		if got := recalledMemoryNotice(content, "result"); got != "" {
			t.Fatalf("ordinary or malformed content must not claim a memory recall: %q", got)
		}
	}
}

func TestMemoryNoticeWrapsWithoutExceedingTerminalWidth(t *testing.T) {
	path, read := memoryReadFixture(t)
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "MEMORY.md"), []byte("- ["+strings.Repeat("记忆标题", 12)+"](sign-off.md) — title\n"), 0600); err != nil {
		t.Fatal(err)
	}
	out := stripANSI(RenderToolCalls(ToolCallsParams{
		ToolCalls: []core.ToolCall{read}, Width: 24,
		ResultMap: map[string]ToolResultData{read.ID: {ToolName: "Read"}},
	}))
	if strings.Count(out, "记") != 12 {
		t.Fatalf("wrapping lost part of the memory title: %q", out)
	}
	for line := range strings.SplitSeq(out, "\n") {
		if lipgloss.Width(line) > 24 {
			t.Fatalf("memory notice exceeds terminal width: %q", line)
		}
	}
}
