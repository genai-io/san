package conv

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/tool/toolresult"
)

// The turn's first committed thinking block leads with the "✦" marker;
// progressively-committed blocks after it align under a blank continuation
// gutter so the glyph appears once, not on every block.
func TestRenderCommittedThinkingBlockGutter(t *testing.T) {
	first := RenderCommittedThinkingBlock("first reasoning block", true, 80, nil)
	if !strings.Contains(first, "✦") {
		t.Fatalf("first thinking block should lead with the ✦ marker, got %q", first)
	}
	cont := RenderCommittedThinkingBlock("continuation reasoning block", false, 80, nil)
	if strings.Contains(cont, "✦") {
		t.Fatalf("continuation thinking block should not repeat the ✦ marker, got %q", cont)
	}
}

// Committed reasoning lays out as markdown — headings and emphasis render rather
// than showing raw "###" / "**" — but flattened to the single muted tone so it
// reads as de-emphasized reasoning.
func TestRenderCommittedThinkingBlockRendersMarkdown(t *testing.T) {
	out := stripANSI(RenderCommittedThinkingBlock("### CoreDNS\n\nDNS service with **bold** text.", true, 80, NewMDRenderer(80)))
	if strings.Contains(out, "###") || strings.Contains(out, "**") {
		t.Errorf("reasoning should render markdown, not raw markers:\n%s", out)
	}
	for _, want := range []string{"CoreDNS", "bold"} {
		if !strings.Contains(out, want) {
			t.Errorf("reasoning text %q should survive:\n%s", want, out)
		}
	}
}

func Test_extractIntField(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		prefix   string
		expected int
	}{
		{
			name:     "valid turns",
			content:  "Agent: Explore\nStatus: completed\nTurns: 12\nTokens: 1500",
			prefix:   "Turns: ",
			expected: 12,
		},
		{
			name:     "turns at start",
			content:  "Turns: 5\nOther info",
			prefix:   "Turns: ",
			expected: 5,
		},
		{
			name:     "large turns number",
			content:  "Some text\nTurns: 999\nMore text",
			prefix:   "Turns: ",
			expected: 999,
		},
		{
			name:     "no turns field",
			content:  "Agent: Explore\nStatus: completed",
			prefix:   "Turns: ",
			expected: 0,
		},
		{
			name:     "empty content",
			content:  "",
			prefix:   "Turns: ",
			expected: 0,
		},
		{
			name:     "turns with zero",
			content:  "Turns: 0\n",
			prefix:   "Turns: ",
			expected: 0,
		},
		{
			name:     "single digit",
			content:  "Turns: 1",
			prefix:   "Turns: ",
			expected: 1,
		},
		{
			name:     "turns followed by text",
			content:  "Turns: 42abc",
			prefix:   "Turns: ",
			expected: 42,
		},
		{
			name:     "extract tokens",
			content:  "Turns: 10\nTokens: 1500",
			prefix:   "Tokens: ",
			expected: 1500,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractIntField(tt.content, tt.prefix)
			if result != tt.expected {
				t.Errorf("extractIntField(%q, %q) = %d, want %d", tt.content, tt.prefix, result, tt.expected)
			}
		})
	}
}

func Test_extractToolArgsPreservesFullCommand(t *testing.T) {
	input := `{"command":"cd /Users/myan/Workspace/ideas/san && git describe --tags --abbrev=0 2>/dev/null"}`
	got := extractToolArgs(input)
	if !strings.Contains(got, "git describe --tags --abbrev=0") {
		t.Fatalf("extractToolArgs() = %q, want full command", got)
	}
}

func Test_renderBashToolCallSingleLineStaysCompact(t *testing.T) {
	out := renderBashToolCall(`{"command":"git status"}`, 100, "●")
	if !strings.Contains(out, "Bash(git status)") {
		t.Fatalf("single-line command should render compact, got %q", out)
	}
	if strings.Contains(out, "│") {
		t.Fatalf("single-line command should not render a command block, got %q", out)
	}
}

func Test_renderBashToolCallMultiLineShowsEveryLine(t *testing.T) {
	input := `{"command":"for f in a b; do\n  echo \"$f\"\ndone","description":"loop over files"}`
	out := renderBashToolCall(input, 100, "●")

	// Every command line renders in the block, not folded away behind ctrl+o.
	for _, want := range []string{"for f in a b; do", `echo "$f"`, "done"} {
		if !strings.Contains(out, want) {
			t.Fatalf("multi-line command should show %q in place, got %q", want, out)
		}
	}
	// The description rides the header as a caption.
	if !strings.Contains(out, "loop over files") {
		t.Fatalf("multi-line command should show its description, got %q", out)
	}
	// The raw command is never crammed into the Bash(...) one-line label.
	if strings.Contains(out, "Bash(for f") {
		t.Fatalf("multi-line command should not be crammed into a one-line label, got %q", out)
	}
}

func Test_renderBashToolCallShowsShellPrompt(t *testing.T) {
	// A long single line wraps into the block, which reads as a terminal command:
	// the first row is led by a "$" prompt, later rows align under the command
	// text without repeating it. No gutter bar, no background fill.
	long := `{"command":"git log --oneline --graph --all --decorate --abbrev-commit --since='2 weeks ago' | head -50"}`
	raw := renderBashToolCall(long, 70, "●")
	if strings.ContainsAny(raw, "│┊") || strings.Contains(raw, "48;2;") {
		t.Fatalf("command block should have no bar and no background, got %q", raw)
	}

	rows := strings.Split(strings.TrimRight(stripANSI(raw), "\n"), "\n")[1:]
	if len(rows) < 2 {
		t.Fatalf("expected the long command to wrap onto multiple rows, got %q", raw)
	}
	if !strings.HasPrefix(rows[0], bashPrompt) {
		t.Fatalf("first command row should carry the %q prompt: %q", bashPrompt, rows[0])
	}
	// Continuations hang under the command text: a blank indent the width of the
	// prompt, so command text lines up down one column and the prompt never repeats.
	contIndent := strings.Repeat(" ", lipgloss.Width(bashPrompt))
	for i, row := range rows[1:] {
		if !strings.HasPrefix(row, contIndent) || strings.HasPrefix(row, bashPrompt) {
			t.Fatalf("continuation row %d should hang under the command text, not repeat the prompt: %q", i+1, row)
		}
	}

	// The "$" sits in the "⎿" result column, so the two markers line up down the left.
	resultPrefix := "  ⎿  "
	if strings.IndexByte(bashPrompt, '$') != strings.Index(resultPrefix, "⎿") {
		t.Fatalf("prompt $ column must equal the result ⎿ column")
	}
	// The command after "$" starts where the result's Bash label starts.
	if lipgloss.Width(bashPrompt) != lipgloss.Width(resultPrefix) {
		t.Fatalf("command indent width = %d, want result label indent %d", lipgloss.Width(bashPrompt), lipgloss.Width(resultPrefix))
	}
}

func TestRenderModeStatusShowsTokenUsageWithModel(t *testing.T) {
	rendered := RenderModeStatus(OperationModeParams{
		ModelName:        "claude-sonnet-4-6",
		InputTokens:      142000,
		InputLimit:       200000,
		ConversationCost: llm.Money{Amount: 0.04, Currency: llm.CurrencyUSD},
		ShowContextBar:   true,
		Width:            120,
	})
	visible := stripANSI(rendered)
	if !strings.Contains(visible, "claude-sonnet-4-6") {
		t.Fatalf("RenderModeStatus() = %q, want model name", visible)
	}
	if !strings.Contains(visible, "[") || !strings.Contains(visible, "] 71%") {
		t.Fatalf("RenderModeStatus() = %q, want bar with percent", visible)
	}
	// The numeric label rides alongside the bar.
	if !strings.Contains(visible, "ctx 142.0k/200.0k") {
		t.Fatalf("RenderModeStatus() = %q, want numeric ctx label", visible)
	}
	// Cost segment must still render.
	if !strings.Contains(visible, "$0.04") {
		t.Fatalf("RenderModeStatus() = %q, want cost segment", visible)
	}
}

// TestRenderModeStatusHidesBarByDefault confirms the visual bar is opt-in:
// with ShowContextBar unset the [██░░] bar and its percent are gone, but the
// numeric "ctx X/Y" label still shows.
func TestRenderModeStatusHidesBarByDefault(t *testing.T) {
	rendered := RenderModeStatus(OperationModeParams{
		ModelName:   "claude-sonnet-4-6",
		InputTokens: 142000,
		InputLimit:  200000,
		Width:       120,
	})
	visible := stripANSI(rendered)
	if !strings.Contains(visible, "ctx 142.0k/200.0k") {
		t.Fatalf("numeric ctx label should still show when bar is off; got %q", visible)
	}
	if strings.Contains(visible, "█") || strings.Contains(visible, "71%") {
		t.Fatalf("visual bar should be hidden by default; got %q", visible)
	}
}

func TestRenderModeStatusShowsBarWhenEnabled(t *testing.T) {
	rendered := RenderModeStatus(OperationModeParams{
		ModelName:      "claude-sonnet-4-6",
		InputTokens:    190000,
		InputLimit:     200000,
		ShowContextBar: true,
		Width:          120,
	})
	visible := stripANSI(rendered)
	if !strings.Contains(visible, "claude-sonnet-4-6") {
		t.Fatalf("want model name in %q", visible)
	}
	// Bar should appear exactly once: fail if EITHER count is wrong.
	if strings.Count(visible, "] ") != 1 || strings.Count(visible, "%") != 1 {
		t.Fatalf("want unified context display (single bar + percent) in %q", visible)
	}
	if !strings.Contains(visible, "compact at") && !strings.Contains(visible, "auto-compact") {
		t.Fatalf("want auto-compact hint in %q", visible)
	}
}

func TestRenderModeStatusShowsCompressionsBadgeWhenNonZero(t *testing.T) {
	rendered := RenderModeStatus(OperationModeParams{
		ModelName:    "claude-sonnet-4-6",
		InputTokens:  1000,
		InputLimit:   200000,
		Compressions: 3,
		Width:        120,
	})
	visible := stripANSI(rendered)
	if !strings.Contains(visible, "compacted ×3") {
		t.Fatalf("want 'compacted ×3' badge in %q", visible)
	}
}

func TestRenderModeStatusHidesBadgeWhenZero(t *testing.T) {
	rendered := RenderModeStatus(OperationModeParams{
		ModelName:    "claude-sonnet-4-6",
		InputTokens:  1000,
		InputLimit:   200000,
		Compressions: 0,
		Width:        120,
	})
	visible := stripANSI(rendered)
	if strings.Contains(visible, "compacted") {
		t.Fatalf("badge should be hidden when zero; got %q", visible)
	}
}

func TestRenderModeStatusShowsPlaceholderWhenLimitUnknown(t *testing.T) {
	// When InputLimit == 0 (limit unknown), the bar must still render with
	// a placeholder so the gap stays visible and actionable, instead of
	// silently hiding the entire context segment.
	rendered := RenderModeStatus(OperationModeParams{
		ModelName:      "some-model",
		InputTokens:    5000,
		InputLimit:     0,
		ShowContextBar: true,
		Width:          120,
	})
	visible := stripANSI(rendered)
	if !strings.Contains(visible, "[----------]") {
		t.Errorf("want placeholder bar [----------] when limit unknown; got %q", visible)
	}
	if !strings.Contains(visible, "--") {
		t.Errorf("want '--' percent label when limit unknown; got %q", visible)
	}
}

func TestRenderModeStatusShowsTemporaryStatusMessage(t *testing.T) {
	rendered := RenderModeStatus(OperationModeParams{
		ModelName:     "kimi-k2.6",
		StatusMessage: "compacted",
		Width:         80,
	})
	for _, want := range []string{"kimi-k2.6", "compacted"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("RenderModeStatus() = %q, want %q", rendered, want)
		}
	}
}

// The bottom status line must carry the ctx readout but never the old
// per-turn "↑… ↓…" usage summary (removed to avoid a second, confusingly
// scoped token figure above the input area).
func TestRenderModeStatusShowsCtxWithoutTurnUsageArrows(t *testing.T) {
	visible := stripANSI(RenderModeStatus(OperationModeParams{
		ModelName:   "gpt-test",
		InputTokens: 164600,
		InputLimit:  272000,
		Width:       120,
	}))
	if !strings.Contains(visible, "ctx") {
		t.Fatalf("RenderModeStatus() = %q, want the ctx label", visible)
	}
	if strings.ContainsAny(visible, "↑↓") {
		t.Fatalf("RenderModeStatus() = %q, must not render per-turn usage arrows", visible)
	}
}

func TestRenderQueuePreviewShowsShadowPromptAndEditHint(t *testing.T) {
	rendered := stripANSI(RenderQueuePreview([]QueuePreviewItem{{
		Content: "Codex review 建议如何运行?",
	}}, -1, 80))

	for _, want := range []string{"❭ Codex review", "↑ edit"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("RenderQueuePreview() = %q, want %q", rendered, want)
		}
	}
	if strings.Contains(rendered, "1.") {
		t.Fatalf("RenderQueuePreview() = %q, should not number items", rendered)
	}
}

func TestRenderQueuePreviewEditingShowsFocusBarAndKeys(t *testing.T) {
	rendered := stripANSI(RenderQueuePreview([]QueuePreviewItem{
		{Content: "first queued"},
		{Content: "second queued"},
	}, 0, 80))

	for _, want := range []string{"▎", "enter done · ctrl+c delete"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("RenderQueuePreview() = %q, want %q", rendered, want)
		}
	}
	if strings.Contains(rendered, "↑ edit") {
		t.Fatalf("RenderQueuePreview() = %q, idle hint should be replaced while editing", rendered)
	}
}

func TestRenderToolCallsWrapsLongBashCommandWithoutTruncating(t *testing.T) {
	const width = 100
	params := ToolCallsParams{
		ToolCalls: []core.ToolCall{{
			ID:    "tc-1",
			Name:  "Bash",
			Input: `{"command":"cd /Users/myan/Workspace/ideas/san && git describe --tags --abbrev=0 2>/dev/null"}`,
		}},
		ResultMap: map[string]ToolResultData{},
		Width:     width,
	}

	rendered := stripANSI(RenderToolCalls(params))

	// A long single-line command wraps into the block form rather than being
	// clipped: the whole command survives, including its tail, and no ellipsis.
	if !strings.Contains(rendered, "git describe --tags --abbrev=0") {
		t.Fatalf("RenderToolCalls() = %q, want the full command", rendered)
	}
	if !strings.Contains(rendered, "2>/dev/null") {
		t.Fatalf("RenderToolCalls() = %q, want the command tail kept, not truncated", rendered)
	}
	if strings.Contains(rendered, "…") {
		t.Fatalf("RenderToolCalls() = %q, want no truncation ellipsis", rendered)
	}
	// Wrapping still honors the 80%-width budget: every line stays within it.
	budget := maxToolLabelWidth(width)
	for _, line := range strings.Split(strings.TrimRight(rendered, "\n"), "\n") {
		if w := lipgloss.Width(line); w > budget {
			t.Fatalf("wrapped line %q width %d exceeds budget %d", line, w, budget)
		}
	}
}

func TestRenderToolCallsShowsRunningStateForPendingBash(t *testing.T) {
	params := ToolCallsParams{
		ToolCalls: []core.ToolCall{{
			ID:    "tc-1",
			Name:  "Bash",
			Input: `{"command":"find /Users/myan -name test"}`,
		}},
		ResultMap: map[string]ToolResultData{},
		PendingCalls: []core.ToolCall{{
			ID:    "tc-1",
			Name:  "Bash",
			Input: `{"command":"find /Users/myan -name test"}`,
		}},
		CurrentIdx:  0,
		SpinnerView: "⋯",
		Width:       100,
	}

	rendered := stripANSI(RenderToolCalls(params))
	if !strings.Contains(rendered, "⋯ Bash(find /Users/myan -name test)") {
		t.Fatalf("RenderToolCalls() = %q, want spinner on the main tool line", rendered)
	}
	if strings.Contains(rendered, "running...") {
		t.Fatalf("RenderToolCalls() = %q, should not add extra running text", rendered)
	}
}

func TestRenderActiveContentShowsRunningStateForPendingWebFetch(t *testing.T) {
	call := core.ToolCall{
		ID:    "tc-1",
		Name:  "WebFetch",
		Input: `{"url":"https://github.com/features/copilot/plans"}`,
	}
	params := RenderContext{
		Messages: []core.ChatMessage{{
			Role:      core.RoleAssistant,
			ToolCalls: []core.ToolCall{call},
		}},
		PendingCalls: []core.ToolCall{call},
		CurrentIdx:   0,
		SpinnerView:  "⋯",
		Width:        100,
	}
	params.InlinedResults = PrecomputeInlinedResults(params.Messages, 0)

	rendered := stripANSI(RenderActiveContent(params))
	if !strings.Contains(rendered, "⋯ WebFetch(https://github.com/features/copilot/plans)") {
		t.Fatalf("RenderActiveContent() = %q, want pending WebFetch spinner", rendered)
	}
}

func TestPrecomputeInlinedResultsFromScopesToActiveRange(t *testing.T) {
	// assistant(0) with a tool call, then its result(1), then a fresh
	// assistant(2) with a call and its result(3).
	msgs := []core.ChatMessage{
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "a", Name: "Bash"}}},
		{Role: core.RoleUser, ToolResult: &core.ToolResult{ToolCallID: "a", ToolName: "Bash"}},
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "b", Name: "Bash"}}},
		{Role: core.RoleUser, ToolResult: &core.ToolResult{ToolCallID: "b", ToolName: "Bash"}},
	}

	// from=0 inlines both results with their owners.
	all := PrecomputeInlinedResults(msgs, 0)
	if !all.IsResultInlined(1) || !all.IsResultInlined(3) {
		t.Fatalf("from=0 should inline both results, got %+v", all.resultOwner)
	}

	// from=2 (first turn committed) scans only the active tail: result(3)
	// still inlines under assistant(2); the committed result(1) is skipped.
	active := PrecomputeInlinedResults(msgs, 2)
	if active.IsResultInlined(1) {
		t.Errorf("from=2 should not scan the committed result at index 1")
	}
	if !active.IsResultInlined(3) || active.ownerOf(3) != 2 {
		t.Errorf("from=2 should still inline the active result at index 3 under assistant 2")
	}
}

func TestRenderToolCallsShowsCompletedIconForResultEvenWhenPending(t *testing.T) {
	call := core.ToolCall{
		ID:    "tc-1",
		Name:  "WebFetch",
		Input: `{"url":"https://github.com/features/copilot/plans"}`,
	}
	params := ToolCallsParams{
		ToolCalls:    []core.ToolCall{call},
		PendingCalls: []core.ToolCall{call},
		CurrentIdx:   0,
		SpinnerView:  "⋯",
		Width:        100,
		ResultMap: map[string]ToolResultData{
			"tc-1": {ToolName: "WebFetch", Content: "done"},
		},
	}

	rendered := stripANSI(RenderToolCalls(params))
	if !strings.Contains(rendered, "● WebFetch(https://github.com/features/copilot/plans)") {
		t.Fatalf("RenderToolCalls() = %q, want completed WebFetch icon", rendered)
	}
	if strings.Contains(rendered, "⋯ WebFetch") {
		t.Fatalf("RenderToolCalls() = %q, should not show spinner for completed result", rendered)
	}
}

func TestRenderToolCallsShowsGapForPendingAgent(t *testing.T) {
	params := ToolCallsParams{
		ToolCalls: []core.ToolCall{{
			ID:    "tc-1",
			Name:  "Agent",
			Input: `{"subagent_type":"Explore","description":"HA code structure","prompt":"Inspect the codebase"}`,
		}},
		ResultMap: map[string]ToolResultData{},
		PendingCalls: []core.ToolCall{{
			ID:    "tc-1",
			Name:  "Agent",
			Input: `{"subagent_type":"Explore","description":"HA code structure","prompt":"Inspect the codebase"}`,
		}},
		CurrentIdx:  0,
		Blink:       agentBlinkTicks,
		SpinnerView: "◓",
		Width:       100,
	}

	rendered := stripANSI(RenderToolCalls(params))
	want := agentIcon(params.Blink) + " Agent - Explorer: HA code structure"
	if !strings.Contains(rendered, want) {
		t.Fatalf("RenderToolCalls() = %q, want a single visible gap before explicit agent label", rendered)
	}
}

func TestRenderToolCallsNamesGeneralAgentByMode(t *testing.T) {
	call := core.ToolCall{
		ID:    "tc-1",
		Name:  "Agent",
		Input: `{"subagent_type":"general-purpose","description":"audit git changes","mode":"explore"}`,
	}
	params := ToolCallsParams{
		ToolCalls:    []core.ToolCall{call},
		ResultMap:    map[string]ToolResultData{},
		PendingCalls: []core.ToolCall{call},
		CurrentIdx:   0,
		Blink:        agentBlinkTicks,
		SpinnerView:  "◓",
		Width:        100,
	}

	rendered := stripANSI(RenderToolCalls(params))
	want := agentIcon(params.Blink) + " Agent - Explorer: audit git changes"
	if !strings.Contains(rendered, want) {
		t.Fatalf("RenderToolCalls() = %q, want mode-based agent label", rendered)
	}
}

func TestRenderToolCallsShowsSingleAgentRuntimeActivity(t *testing.T) {
	call := core.ToolCall{
		ID:    "tc-1",
		Name:  "Agent",
		Input: `{"subagent_type":"Explore","description":"audit git changes before review","prompt":"Inspect the codebase","mode":"explore"}`,
	}
	params := ToolCallsParams{
		ToolCalls:    []core.ToolCall{call},
		ResultMap:    map[string]ToolResultData{},
		PendingCalls: []core.ToolCall{call},
		CurrentIdx:   0,
		TaskActivity: map[int][]string{
			0: {
				"Mode: explore · max 100 turns",
				"Thinking...",
				"Read(internal/tool/schema_agent.go)",
				"Grep(ContinueAgent)",
				"Read(internal/app/conv/message.go)",
				"Grep(renderAgentActivityInline)",
			},
		},
		ModelName:    "gpt-5.4-mini",
		InputTokens:  18500,
		OutputTokens: 467,
		Blink:        agentBlinkTicks,
		SpinnerView:  "◓",
		Width:        120,
	}

	rendered := stripANSI(RenderToolCalls(params))
	want := agentIcon(params.Blink) + " Agent - Explorer: audit git changes before review"
	if !strings.Contains(rendered, want) {
		t.Fatalf("RenderToolCalls() = %q, want agent header", rendered)
	}
	if !strings.Contains(rendered, "model: gpt-5.4-mini") || !strings.Contains(rendered, "mode: explore") || !strings.Contains(rendered, "tools: 4") {
		t.Fatalf("RenderToolCalls() = %q, want runtime summary", rendered)
	}
	if strings.Contains(rendered, "Read(internal/tool/schema_agent.go)") {
		t.Fatalf("RenderToolCalls() = %q, want only the latest three tool calls", rendered)
	}
	for _, want := range []string{"Grep(ContinueAgent)", "Read(internal/app/conv/message.go)", "Grep(renderAgentActivityInline)"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("RenderToolCalls() = %q, missing recent tool call %q", rendered, want)
		}
	}
}

func TestRenderToolCallsShowsAgentStatusBeforeToolCalls(t *testing.T) {
	call := core.ToolCall{
		ID:    "tc-1",
		Name:  "Agent",
		Input: `{"subagent_type":"Explore","description":"audit git changes","prompt":"Inspect the codebase","mode":"explore"}`,
	}
	params := ToolCallsParams{
		ToolCalls:    []core.ToolCall{call},
		ResultMap:    map[string]ToolResultData{},
		PendingCalls: []core.ToolCall{call},
		CurrentIdx:   0,
		TaskActivity: map[int][]string{
			0: {
				"Mode: explore · max 100 turns",
				"Thinking...",
			},
		},
		ModelName:   "gpt-5.4-mini",
		SpinnerView: "◓",
		Width:       120,
	}

	rendered := stripANSI(RenderToolCalls(params))
	if !strings.Contains(rendered, "model: gpt-5.4-mini") || !strings.Contains(rendered, "mode: explore") {
		t.Fatalf("RenderToolCalls() = %q, want runtime summary before tool calls", rendered)
	}
	if !strings.Contains(rendered, "Thinking...") {
		t.Fatalf("RenderToolCalls() = %q, want status before tool calls", rendered)
	}
}

func TestRenderToolCallsUsesActivityModelForAgentSummary(t *testing.T) {
	call := core.ToolCall{
		ID:    "tc-1",
		Name:  "Agent",
		Input: `{"subagent_type":"Explore","description":"audit git changes","prompt":"Inspect the codebase","mode":"explore","model":"sonnet"}`,
	}
	params := ToolCallsParams{
		ToolCalls:    []core.ToolCall{call},
		ResultMap:    map[string]ToolResultData{},
		PendingCalls: []core.ToolCall{call},
		CurrentIdx:   0,
		TaskActivity: map[int][]string{
			0: {
				"Model: gpt-5.5",
				"Mode: explore · max 100 turns",
				"Thinking...",
			},
		},
		ModelName:   "gpt-5.4-mini",
		SpinnerView: "◓",
		Width:       120,
	}

	rendered := stripANSI(RenderToolCalls(params))
	if !strings.Contains(rendered, "model: gpt-5.5") {
		t.Fatalf("RenderToolCalls() = %q, want activity model", rendered)
	}
	if strings.Contains(rendered, "model: sonnet") {
		t.Fatalf("RenderToolCalls() = %q, should not use raw tool input model", rendered)
	}
}

func TestRenderToolCallsUsesActivityUsageForAgentTokens(t *testing.T) {
	call := core.ToolCall{
		ID:    "tc-1",
		Name:  "Agent",
		Input: `{"subagent_type":"general-purpose","description":"audit git changes","mode":"explore"}`,
	}
	params := ToolCallsParams{
		ToolCalls:    []core.ToolCall{call},
		ResultMap:    map[string]ToolResultData{},
		PendingCalls: []core.ToolCall{call},
		CurrentIdx:   0,
		TaskActivity: map[int][]string{
			0: {
				"Model: kimi-k2.6",
				"Mode: explore · max 100 turns",
				"Usage: input=8300 output=272",
				"Read(README.md)",
				"Usage: input=9200 output=410",
			},
		},
		ModelName:    "gpt-5.4-mini",
		InputTokens:  100,
		OutputTokens: 10,
		SpinnerView:  "◓",
		Width:        120,
	}

	rendered := stripANSI(RenderToolCalls(params))
	if !strings.Contains(rendered, "tokens: ↑9.2k ↓410") {
		t.Fatalf("RenderToolCalls() = %q, want latest activity token usage", rendered)
	}
	if strings.Contains(rendered, "tools: 3") {
		t.Fatalf("RenderToolCalls() = %q, usage lines should not count as tools", rendered)
	}
}

func Test_formatToolResultSizeUsesNoOutputForEmptyContent(t *testing.T) {
	if got := formatToolResultSize("Bash", ""); got != "no output" {
		t.Fatalf("formatToolResultSize() = %q, want %q", got, "no output")
	}
}

func TestRenderToolResultInlineShowsEditSummary(t *testing.T) {
	details := toolresult.EditDetails{
		Path:         "internal/app/view.go",
		EditCount:    2,
		AddedLines:   3,
		RemovedLines: 1,
		UnifiedDiff:  "@@ -1 +1 @@\n-old\n+new",
	}
	result := stripANSI(RenderToolResultInline(ToolResultData{ToolName: "Edit", Details: details}, nil))
	if !strings.Contains(result, "2 replacements · +3 -1") {
		t.Fatalf("successful Edit summary = %q", result)
	}
	if strings.Contains(result, "-old") {
		t.Fatalf("collapsed Edit should not show the diff, got %q", result)
	}

	expanded := stripANSI(RenderToolResultInline(ToolResultData{ToolName: "Edit", Details: details, Expanded: true}, nil))
	if !strings.Contains(expanded, "-old") || !strings.Contains(expanded, "+new") {
		t.Fatalf("expanded Edit should show the final diff, got %q", expanded)
	}

	errResult := stripANSI(RenderToolResultInline(ToolResultData{ToolName: "Edit", Content: "Error: edits[0]: oldText was not found", IsError: true}, nil))
	if !strings.Contains(errResult, "Edit → failed") {
		t.Fatalf("Edit error summary should show failure, got %q", errResult)
	}
	if strings.Contains(errResult, "Edit → Error:") || strings.Contains(errResult, "Error: edits") {
		t.Fatalf("Edit error should not repeat the error prefix, got %q", errResult)
	}
	if !strings.Contains(errResult, "\n     edits[0]: oldText was not found\n") {
		t.Fatalf("Edit error reason should align with Edit, got %q", errResult)
	}

	bashErrResult := stripANSI(RenderToolResultInline(ToolResultData{ToolName: "Bash", Content: "command failed", IsError: true}, nil))
	if !strings.Contains(bashErrResult, "\n     command failed\n") {
		t.Fatalf("Bash error reason should align with Bash, got %q", bashErrResult)
	}
}

func TestRenderToolCallsShowsEditResult(t *testing.T) {
	call := core.ToolCall{
		ID:    "edit-1",
		Name:  "Edit",
		Input: `{"path":"internal/app/view.go","edits":[{"oldText":"old","newText":"new"}]}`,
	}
	rendered := stripANSI(RenderToolCalls(ToolCallsParams{
		ToolCalls: []core.ToolCall{call},
		ResultMap: map[string]ToolResultData{
			call.ID: {ToolName: "Edit", Content: "Edited internal/app/view.go"},
		},
		Width: 100,
	}))
	if !strings.Contains(rendered, "Edit(internal/app/view.go)") {
		t.Fatalf("completed Edit call should remain visible, got %q", rendered)
	}
	if !strings.Contains(rendered, "Edit →") {
		t.Fatalf("Edit result should be visible, got %q", rendered)
	}
}

func Test_renderTaskOutputResultInlineShowsErrorText(t *testing.T) {
	rendered := renderTaskOutputResultInline(ToolResultData{
		ToolName: "TaskOutput",
		IsError:  true,
		Content:  "task not found: 10f7b381",
	})

	if !strings.Contains(rendered, "TaskOutput → Error") {
		t.Fatalf("expected TaskOutput error header, got %q", rendered)
	}
	if !strings.Contains(rendered, "task not found: 10f7b381") {
		t.Fatalf("expected TaskOutput error text, got %q", rendered)
	}
}

func TestRenderDecision(t *testing.T) {
	// No decision → no annotation line at all.
	if got := renderDecision(nil); got != "" {
		t.Errorf("nil decision should render nothing, got %q", got)
	}

	approved := stripANSI(renderDecision(&core.ReviewDecision{Approved: true, Reason: "read-only ls, no side effects"}))
	for _, want := range []string{"↳", "auto-approved", "read-only ls, no side effects"} {
		if !strings.Contains(approved, want) {
			t.Errorf("approved decision missing %q: %q", want, approved)
		}
	}

	escalated := stripANSI(renderDecision(&core.ReviewDecision{Approved: false, Reason: "writes outside the project"}))
	if !strings.Contains(escalated, "escalated") {
		t.Errorf("escalated decision should say so: %q", escalated)
	}

	// A blank reason drops the separator rather than leaving a dangling "·".
	bare := stripANSI(renderDecision(&core.ReviewDecision{Approved: true}))
	if strings.Contains(bare, "·") {
		t.Errorf("blank reason should not render a separator: %q", bare)
	}
}
