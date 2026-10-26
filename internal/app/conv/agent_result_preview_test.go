package conv

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool/toolresult"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestAgentResultPreviewBudget(t *testing.T) {
	for _, width := range []int{32, 40, 100} {
		for _, structured := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/structured=%t", width, structured), func(t *testing.T) {
				body := "First finding\n\nSecond finding\n\nThird finding\n\nFourth finding\n\nFifth finding\nHIDDEN_TAIL_SENTINEL"
				data := ToolResultData{ToolName: "Agent", Content: body + "\n\nAgentID: child\n\nTranscript: /tmp/child.jsonl", Width: width, ToolInput: `{"name":"reviewer","description":"Review security boundaries"}`}
				if structured {
					data.Details = toolresult.AgentDetails{ToolUses: 7, Model: "private-model", Activity: []string{"Read(private.go)"}}
				} else {
					data.Content = "Agent: reviewer\nToolUses: 7\nModel: private-model\nProcess: 0\n\n" + data.Content
				}
				got := ansi.Strip(RenderToolResultInline(data, nil))
				rows := previewNonblankRows(got)
				if len(rows) != 4 {
					t.Fatalf("want four nonblank physical rows, got %d: %q", len(rows), got)
				}
				for _, row := range rows {
					if ansi.StringWidth(row) > width {
						t.Fatalf("overflow at width %d: %q", width, row)
					}
				}
				if !strings.HasSuffix(rows[3], "…") || strings.TrimSpace(strings.TrimSuffix(rows[3], "…")) == "" {
					t.Fatalf("ellipsis must follow visible prose on final row: %q", got)
				}
				for _, absent := range []string{"HIDDEN_TAIL_SENTINEL", "Fifth", "AgentID:", "Transcript:", "ToolUses:", "private-model", "private.go", "omitted", "Finished", "Done"} {
					if strings.Contains(got, absent) {
						t.Errorf("compact leaked %q: %q", absent, got)
					}
				}
				data.Expanded = true
				expanded := ansi.Strip(RenderToolResultInline(data, NewMDRenderer(width)))
				if !strings.Contains(strings.Join(strings.Fields(strings.ReplaceAll(expanded, "┊", "")), ""), "HIDDEN_TAIL_SENTINEL") {
					t.Fatalf("expanded result lost hidden tail: %q", expanded)
				}
			})
		}
	}
}

func TestAgentResultPreviewMarkdownAndColor(t *testing.T) {
	wasDark := kit.IsDarkBackground()
	t.Cleanup(func() {
		if wasDark {
			kit.InitTheme("dark")
		} else {
			kit.InitTheme("light")
		}
	})
	for _, theme := range []string{"light", "dark"} {
		kit.InitTheme(theme)
		for _, width := range []int{32, 40, 100} {
			t.Run(fmt.Sprintf("%s/%d", theme, width), func(t *testing.T) {
				call := core.ToolCall{ID: "a", Name: "Agent", Input: `{"name":"reviewer","description":"Security review"}`}
				params := ToolCallsParams{ToolCalls: []core.ToolCall{call}, Width: width, MDRenderer: NewMDRenderer(width), ResultMap: map[string]ToolResultData{"a": {ToolName: "Agent", Content: "Plain finding", Details: toolresult.AgentDetails{}}}}
				raw := RenderToolCalls(params)
				if !strings.Contains(raw, toolResultStyle.Render("  Plain finding")) || toolResultStyle.Render("  Plain finding") == toolCallStyle.Render("  Plain finding") {
					t.Errorf("actual preview must use dim result color, not header color: %q", raw)
				}
				if !strings.Contains(raw, toolCallStyle.Render("Agent")) && !strings.Contains(raw, toolCallStyle.Render("Agent · reviewer · Security review")) {
					// The header can be emitted as one styled row rather than styled fragments.
					first, _, _ := strings.Cut(raw, "\n")
					if !strings.Contains(first, "\x1b[") || strings.Contains(first, toolResultStyle.Render(ansi.Strip(first))) {
						t.Fatalf("header lost distinct ANSI styling: %q", first)
					}
				}
				params.ResultMap["a"] = ToolResultData{ToolName: "Agent", Content: "## Findings\n\n**Boundary** uses `guard` correctly.\n\n- Safe outcome", Details: toolresult.AgentDetails{}}
				got := ansi.Strip(RenderToolCalls(params))
				for _, marker := range []string{"##", "**", "`"} {
					if strings.Contains(got, marker) {
						t.Errorf("raw markdown %q remains: %q", marker, got)
					}
				}
				if !strings.Contains(got, "Findings") || !strings.Contains(got, "Boundary") {
					t.Fatalf("renderer lost original findings: %q", got)
				}
				_, body, _ := strings.Cut(got, "\n")
				if rows := previewNonblankRows(body); len(rows) > 4 {
					t.Fatalf("rendered markdown exceeds budget: %q", got)
				}
			})
		}
	}
}

func TestAgentResultPreviewIdentityExpansion(t *testing.T) {
	const description = "Review execution and approval consistency across all permission boundaries"
	call := core.ToolCall{ID: "a", Name: "Agent", Input: `{"name":"reviewer","description":"` + description + `"}`}
	for _, width := range []int{32, 40, 100} {
		params := ToolCallsParams{ToolCalls: []core.ToolCall{call}, Width: width, ResultMap: map[string]ToolResultData{"a": {ToolName: "Agent", Content: "Useful result", Details: toolresult.AgentDetails{}}}}
		got := ansi.Strip(RenderToolCalls(params))
		header, body, _ := strings.Cut(got, "\n")
		if !strings.HasPrefix(header, "● Agent · reviewer · Review") || ansi.StringWidth(header) > width || strings.TrimSpace(body) != "Useful result" {
			t.Fatalf("completed identity should occupy one bounded row: %q", got)
		}
		if width < 100 && !strings.HasSuffix(header, "…") {
			t.Fatalf("long identity missing inline truncation: %q", header)
		}
		params.ToolCallsExpanded = true
		data := params.ResultMap[call.ID]
		data.Expanded = true
		params.ResultMap[call.ID] = data
		expanded := ansi.Strip(RenderToolCalls(params))
		if !strings.Contains(strings.Join(strings.Fields(expanded), ""), strings.ReplaceAll(description, " ", "")) || !strings.Contains(expanded, "reviewer") || !strings.Contains(expanded, "Useful result") {
			t.Fatalf("expanded identity or body lost: %q", expanded)
		}
	}
}

func TestParallelAgentResultPreviewOrderAndSpacing(t *testing.T) {
	for _, width := range []int{32, 40, 100} {
		calls := []core.ToolCall{}
		for _, id := range []string{"A", "B", "C"} {
			calls = append(calls, core.ToolCall{ID: id, Name: "Agent", Input: `{"name":"worker` + id + `","description":"Review ` + id + `"}`})
		}
		messages := []core.ChatMessage{{Role: core.ChatAssistant, ToolCalls: calls}}
		for _, id := range []string{"C", "A", "B"} {
			messages = append(messages, core.ChatMessage{Role: core.ChatUser, ToolDetails: toolresult.AgentDetails{}, ToolResult: &core.ToolResult{ToolCallID: id, ToolName: "Agent", Content: ai.TextContent("Finding " + id)}})
		}
		messages = append(messages, core.ChatMessage{Role: core.ChatAssistant, Content: "Parent conclusion"})
		ctx := RenderContext{Width: width, Messages: messages, MDRenderer: NewMDRenderer(width), InlinedResults: PrecomputeInlinedResults(messages, 0)}
		got := ansi.Strip(RenderMessageRange(ctx, 0, len(messages), false))
		last := -1
		for _, id := range []string{"A", "B", "C"} {
			header := strings.Index(got, "Agent · worker"+id)
			result := strings.Index(got, "Finding "+id)
			if header <= last || result <= header || strings.Count(got, "Finding "+id) != 1 {
				t.Fatalf("width %d: result must follow original owner once: %q", width, got)
			}
			if last >= 0 && !strings.Contains(got[last:header], "\n\n") {
				t.Fatalf("adjacent Agent blocks need blank separator: %q", got)
			}
			last = result
		}
		if strings.Index(got, "Parent conclusion") <= last {
			t.Fatalf("parent conclusion must follow Agent groups: %q", got)
		}
	}
}

func previewNonblankRows(s string) []string {
	var rows []string
	for _, row := range strings.Split(s, "\n") {
		if strings.TrimSpace(row) != "" {
			rows = append(rows, row)
		}
	}
	return rows
}
