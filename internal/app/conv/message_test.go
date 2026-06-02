package conv

import (
	"strings"
	"testing"

	"github.com/genai-io/gen-code/internal/core"
	"github.com/genai-io/gen-code/internal/llm"
)

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
	input := `{"command":"cd /Users/myan/Workspace/ideas/gencode && git describe --tags --abbrev=0 2>/dev/null"}`
	got := extractToolArgs(input)
	if !strings.Contains(got, "git describe --tags --abbrev=0") {
		t.Fatalf("extractToolArgs() = %q, want full command", got)
	}
}

func TestRenderModeStatusShowsTokenUsageWithModel(t *testing.T) {
	rendered := RenderModeStatus(OperationModeParams{
		ModelName:        "gpt-test",
		InputTokens:      1234,
		OutputTokens:     56,
		InputLimit:       10000,
		ConversationCost: llm.Money{Amount: 0.1234, Currency: llm.CurrencyCNY},
		Width:            100,
	})

	for _, want := range []string{"gpt-test", "1.2k/10.0k (12%)", "¥0.123"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("RenderModeStatus() = %q, want %q", rendered, want)
		}
	}
	for _, unwanted := range []string{"↑1.2k", "↓56"} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("RenderModeStatus() = %q, should not contain per-turn usage %q", rendered, unwanted)
		}
	}
	if !strings.Contains(rendered, " · ") {
		t.Fatalf("RenderModeStatus() = %q, want segmented display", rendered)
	}
}

func TestRenderModeStatusKeepsContextDisplayOnRightOnly(t *testing.T) {
	rendered := RenderModeStatus(OperationModeParams{
		ModelName:      "kimi-k2.6",
		InputTokens:    301800,
		InputLimit:     262100,
		ThinkingEffort: "think+",
		ShowThinking:   true,
		Width:          120,
	})

	if !strings.Contains(rendered, "kimi-k2.6") {
		t.Fatalf("RenderModeStatus() = %q, want model name", rendered)
	}
	if !strings.Contains(rendered, "301.8k/262.1k (115%)") {
		t.Fatalf("RenderModeStatus() = %q, want unified context display on the right", rendered)
	}
	if !strings.Contains(rendered, "auto-compact") {
		t.Fatalf("RenderModeStatus() = %q, want auto-compact hint", rendered)
	}
	if strings.Count(rendered, "115%") != 1 {
		t.Fatalf("RenderModeStatus() = %q, should only show context percentage once", rendered)
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

func TestRenderTurnUsageSummaryShowsPerTurnTokens(t *testing.T) {
	rendered := RenderTurnUsageSummary(1234, 56, 80)
	for _, want := range []string{"↑1.2k", "↓56"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("RenderTurnUsageSummary() = %q, want %q", rendered, want)
		}
	}
}

func TestRenderModeStatusHidesQueueBadgeWhenEmpty(t *testing.T) {
	rendered := RenderModeStatus(OperationModeParams{
		ModelName:  "gpt-test",
		QueueCount: 0,
		Width:      80,
	})
	if strings.Contains(rendered, "queued") {
		t.Fatalf("RenderModeStatus() = %q, should not show queue badge", rendered)
	}
}

func TestRenderQueuePreviewShowsItems(t *testing.T) {
	rendered := stripANSI(RenderQueuePreview([]QueuePreviewItem{{
		Content: "Codex review 建议如何运行?",
	}}, -1, 80))

	for _, want := range []string{"Codex review"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("RenderQueuePreview() = %q, want %q", rendered, want)
		}
	}
}

func TestRenderToolCallsUsesEightyPercentWidth(t *testing.T) {
	params := ToolCallsParams{
		ToolCalls: []core.ToolCall{{
			ID:    "tc-1",
			Name:  "Bash",
			Input: `{"command":"cd /Users/myan/Workspace/ideas/gencode && git describe --tags --abbrev=0 2>/dev/null"}`,
		}},
		ResultMap: map[string]ToolResultData{},
		Width:     100,
	}

	rendered := RenderToolCalls(params)
	if !strings.Contains(rendered, "git describe --tags --abbrev") {
		t.Fatalf("RenderToolCalls() = %q, want wider command preview", rendered)
	}
	if !strings.Contains(rendered, "...") {
		t.Fatalf("RenderToolCalls() = %q, want truncation at 80%% width", rendered)
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

	rendered := RenderToolCalls(params)
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
	params.InlinedResults = PrecomputeInlinedResults(params.Messages)

	rendered := RenderActiveContent(params)
	if !strings.Contains(rendered, "⋯ WebFetch(https://github.com/features/copilot/plans)") {
		t.Fatalf("RenderActiveContent() = %q, want pending WebFetch spinner", rendered)
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

	rendered := RenderToolCalls(params)
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

	rendered := RenderToolCalls(params)
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

func TestRenderToolCallsShowsSingleAgentRuntimeProgress(t *testing.T) {
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
		TaskProgress: map[int][]string{
			0: {
				"Mode: explore · max 100 turns",
				"Thinking...",
				"Read(internal/tool/schema_agent.go)",
				"Grep(ContinueAgent)",
				"Read(internal/app/conv/message.go)",
				"Grep(renderAgentProgressInline)",
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
	for _, want := range []string{"Grep(ContinueAgent)", "Read(internal/app/conv/message.go)", "Grep(renderAgentProgressInline)"} {
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
		TaskProgress: map[int][]string{
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

func TestRenderToolCallsUsesProgressModelForAgentSummary(t *testing.T) {
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
		TaskProgress: map[int][]string{
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
		t.Fatalf("RenderToolCalls() = %q, want progress model", rendered)
	}
	if strings.Contains(rendered, "model: sonnet") {
		t.Fatalf("RenderToolCalls() = %q, should not use raw tool input model", rendered)
	}
}

func TestRenderToolCallsUsesProgressUsageForAgentTokens(t *testing.T) {
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
		TaskProgress: map[int][]string{
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
		t.Fatalf("RenderToolCalls() = %q, want latest progress token usage", rendered)
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

func Test_renderTaskOutputResultInlineShowsErrorText(t *testing.T) {
	rendered := renderTaskOutputResultInline(ToolResultData{
		ToolName: "TaskOutput",
		IsError:  true,
		Error:    "task not found: 10f7b381",
	})

	if !strings.Contains(rendered, "TaskOutput → Error") {
		t.Fatalf("expected TaskOutput error header, got %q", rendered)
	}
	if !strings.Contains(rendered, "task not found: 10f7b381") {
		t.Fatalf("expected TaskOutput error text, got %q", rendered)
	}
}
