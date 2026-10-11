package conv

import (
	"strings"
	"testing"

	"github.com/genai-io/san/internal/tool/toolresult"
)

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
			if len(collapsed) > 300 || !strings.Contains(collapsed, "exit code 1") {
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
		if !strings.Contains(collapsed, status+" · provider unavailable") || !strings.Contains(collapsed, "125 tokens") || strings.Contains(collapsed, "partial findings") {
			t.Fatalf("structured failure has wrong collapsed state: %q", collapsed)
		}
		data.Expanded = true
		expanded := stripANSI(RenderToolResultInline(data, nil))
		for _, want := range []string{"Read(main.go)", "partial findings", "/tmp/child.jsonl"} {
			if !strings.Contains(expanded, want) {
				t.Fatalf("unfinished result lost %q: %q", want, expanded)
			}
		}
	}
}

func TestLegacyAgentUsageUnderstandsBothTokenFormats(t *testing.T) {
	for _, tokens := range []string{"125", "in=100 out=25"} {
		data := ToolResultData{ToolName: "Agent", Content: "Agent: Explore\nToolUses: 1\nTokens: " + tokens + "\nDuration: 1s\n\nFinding"}
		if got := stripANSI(RenderToolResultInline(data, nil)); !strings.Contains(got, "125 tokens") {
			t.Fatalf("legacy usage disappeared for %q: %q", tokens, got)
		}
	}
}
