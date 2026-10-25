package conv

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool/toolresult"
)

func TestLiveAgentErrorsAreBoundedUntilExpanded(t *testing.T) {
	call := core.ToolCall{ID: "worker", Name: "Agent", Input: `{"prompt":"Review"}`}
	activity := map[int][]string{0: {"Read(main.go) failed: " + strings.Repeat("诊断 details\n", 1000)}}
	for _, width := range []int{30, 80} {
		params := ToolCallsParams{ToolCalls: []core.ToolCall{call}, PendingCalls: []core.ToolCall{call}, ToolStartedAt: map[string]time.Time{call.ID: time.Now()}, TaskActivity: activity, Width: width}
		collapsed := stripANSI(RenderToolCalls(params))
		spinner := stripANSI(RenderPendingToolSpinner(PendingToolSpinnerParams{PendingCalls: params.PendingCalls, ToolStartedAt: params.ToolStartedAt, TaskActivity: activity, Width: width}))
		for _, view := range []string{collapsed, spinner} {
			if strings.Count(view, "\n") > 6 || !strings.Contains(view, "failed") {
				t.Fatalf("width %d: live error occupies %d lines or loses its status", width, strings.Count(view, "\n"))
			}
			for line := range strings.SplitSeq(view, "\n") {
				if ansi.StringWidth(line) > width {
					t.Fatalf("width %d: activity row overflows: %q", width, line)
				}
			}
		}
		params.ToolCallsExpanded = true
		if expanded := stripANSI(RenderToolCalls(params)); strings.Count(strings.Join(strings.Fields(expanded), ""), "诊断details") != 1000 {
			t.Fatal("expansion lost the full diagnostics")
		}
	}
}

func TestFailedToolBodiesRespectExpansion(t *testing.T) {
	output := strings.Repeat("diagnostic output\n", 1000)
	for _, nested := range []bool{false, true} {
		for _, name := range []string{"Bash", "Agent", "WebSearch"} {
			data := ToolResultData{ToolName: name, Nested: nested, Width: 80,
				Content: output + "Error: exit code 1", IsError: true}
			if name == "Bash" {
				data.Details = toolresult.BashDetails{Error: "exit code 1", LineCount: 1000}
			}
			collapsed := stripANSI(RenderToolResultInline(data, nil))
			// A Bash failure also keeps its output's end, bounded in rows.
			bounded := len(collapsed) <= 300
			if name == "Bash" {
				bounded = strings.Count(collapsed, "\n") <= setting.MaxBashPreviewLines+1
			}
			if !bounded || !strings.Contains(collapsed, "exit code 1") {
				t.Errorf("%s nested=%v must show a bounded reason; rendered %d bytes", name, nested, len(collapsed))
			}
			data.Expanded = true
			expanded := stripANSI(RenderToolResultInline(data, nil))
			if strings.Count(expanded, "diagnostic output") != 1000 {
				t.Errorf("%s nested=%v lost full diagnostics on expansion", name, nested)
			}
		}
	}
}

func TestStructuredAgentFailureKeepsActivityAndPartialResults(t *testing.T) {
	for _, stopped := range []bool{false, true} {
		data := ToolResultData{ToolName: "Agent", Content: "partial findings\nTranscript: /tmp/child.jsonl\nError: provider unavailable", IsError: true, Nested: true, Width: 80,
			Details: toolresult.AgentDetails{Stopped: stopped, Error: "provider unavailable", Activity: []string{"Read(main.go)"}, InputTokens: 100, OutputTokens: 25},
		}
		collapsed := stripANSI(RenderToolResultInline(data, nil))
		status := "Failed"
		if stopped {
			status = "Stopped"
		}
		if !strings.Contains(collapsed, status+" · provider unavailable") || strings.Contains(collapsed, "125 tokens") || strings.Contains(collapsed, "partial findings") {
			t.Fatalf("structured failure has wrong collapsed state: %q", collapsed)
		}
		data.Expanded = true
		expanded := stripANSI(RenderToolResultInline(data, nil))
		for _, want := range []string{"Read(main.go)", "partial findings", "/tmp/child.jsonl", "125 tokens"} {
			if !strings.Contains(expanded, want) {
				t.Fatalf("unfinished result lost %q: %q", want, expanded)
			}
		}
	}
}

func TestCompletedAgentMetadataRequiresExpansion(t *testing.T) {
	for _, nested := range []bool{false, true} {
		for _, structured := range []bool{false, true} {
			data := ToolResultData{ToolName: "Agent", Nested: nested, Width: 80,
				Content: "Agent: Explore\nToolUses: 3\nTokens: 125\nDuration: 2.7s\nModel: test-model\n\nFinding"}
			if structured {
				data.Content = "Finding"
				data.Details = toolresult.AgentDetails{ToolUses: 3, InputTokens: 100, OutputTokens: 25, Model: "test-model"}
			}
			collapsed := stripANSI(RenderToolResultInline(data, nil))
			for _, metadata := range []string{"tool uses", "tokens", "2.7s", "test-model"} {
				if strings.Contains(collapsed, metadata) {
					t.Fatalf("default completion leaks %q: %q", metadata, collapsed)
				}
			}
			data.Expanded = true
			expanded := stripANSI(RenderToolResultInline(data, nil))
			for _, want := range []string{"Finding", "3 tool uses", "125 tokens", "test-model"} {
				if !strings.Contains(expanded, want) {
					t.Fatalf("expanded completion lost %q: %q", want, expanded)
				}
			}
		}
	}
}

func TestLegacyAgentUsageUnderstandsBothTokenFormats(t *testing.T) {
	for _, tokens := range []string{"125", "in=100 out=25"} {
		data := ToolResultData{ToolName: "Agent", Expanded: true, Content: "Agent: Explore\nToolUses: 1\nTokens: " + tokens + "\nDuration: 1s\n\nFinding"}
		if got := stripANSI(RenderToolResultInline(data, nil)); !strings.Contains(got, "125 tokens") {
			t.Fatalf("legacy usage disappeared for %q: %q", tokens, got)
		}
	}
}
