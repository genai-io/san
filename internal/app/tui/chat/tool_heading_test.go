package chat

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/core"
)

func TestToolFailureKeepsPathAndBoundsDiagnostics(t *testing.T) {
	call := core.ToolCall{ID: "read", Name: "Read", Input: `{"file_path":"internal/app/tui/chat/message.go"}`}
	params := ToolCallsParams{ToolCalls: []core.ToolCall{call}, Width: 48, ResultMap: map[string]ToolResultData{}}
	const heading = "● Read internal/app/tui/chat/message.go · Failed\n"
	for _, tc := range []struct{ content, preview string }{
		{"Error: permission denied", "  ┊ permission denied\n"},
		{"Error: permission denied\nFailed to open file\nCheck parent permissions\nFinal diagnostic", "  ┊ permission denied\n  ┊ Failed to open file\n  ┊ Check parent permissions\n  ┊ … more diagnostics\n"},
		{"Error: " + strings.Repeat("X", 44*3+1), strings.Repeat("  ┊ "+strings.Repeat("X", 44)+"\n", 3) + "  ┊ … more diagnostics\n"},
	} {
		result := ToolResultData{ToolName: call.Name, IsError: true, Content: tc.content}
		params.ResultMap[call.ID] = result
		if got := ansi.Strip(RenderToolCalls(params)); got != heading+tc.preview {
			t.Errorf("compact diagnostic:\n%s\nwant:\n%s", got, heading+tc.preview)
		}
		result.Expanded = true
		params.ResultMap[call.ID] = result
		expanded := ansi.Strip(RenderToolCalls(params))
		if strings.Contains(expanded, "more diagnostics") || strings.Contains(tc.content, "Final diagnostic") && !strings.Contains(expanded, "Final diagnostic") || strings.Count(expanded, "X") != strings.Count(tc.content, "X") {
			t.Errorf("expanded diagnostic lost original lines: %q", expanded)
		}
	}
}

func TestCompactToolHeadingNarrowTerminal(t *testing.T) {
	call := core.ToolCall{ID: "read", Name: "Read", Input: `{"file_path":"目录/a  b.go"}`}
	params := ToolCallsParams{
		ToolCalls: []core.ToolCall{call}, Width: 20,
		ResultMap: map[string]ToolResultData{call.ID: {ToolName: "Read", Content: "1\tpackage main"}},
	}
	if got := ansi.Strip(RenderToolCalls(params)); got != "● Read 目录/a  b.go\n" {
		t.Fatalf("optional count displaced the exact path: %q", got)
	}
	params.ResultMap[call.ID] = ToolResultData{ToolName: "Read", IsError: true, Content: "Error: permission denied"}
	got := ansi.Strip(RenderToolCalls(params))
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(lines) != 3 || !strings.HasSuffix(lines[0], " · Failed") || lines[1] != "  ┊ permission" || lines[2] != "  ┊ denied" {
		t.Fatalf("status or diagnostic lost at narrow width: %q", got)
	}
	for _, line := range lines {
		if ansi.StringWidth(line) > params.Width {
			t.Fatalf("overflow at width %d: %q", params.Width, line)
		}
	}
}
