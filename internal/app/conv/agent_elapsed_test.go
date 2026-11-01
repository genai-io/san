package conv

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool/toolresult"
)

func TestAgentElapsedTitleWidth(t *testing.T) {
	for _, width := range []int{8, 16, 32, 40, 80} {
		for _, compact := range []bool{false, true} {
			for _, expanded := range []bool{false, true} {
				call := core.ToolCall{ID: "worker", Name: "Agent", Input: `{"name":"审查员","description":"Review execution and approval consistency"}`}
				params := ToolCallsParams{ToolCalls: []core.ToolCall{call}, PendingCalls: []core.ToolCall{call}, Width: width, CompactAgentLabel: compact, ToolCallsExpanded: expanded, AgentPreviewLines: 1, ToolStartedAt: map[string]time.Time{call.ID: time.Now().Add(-12 * time.Second)}, TaskActivity: map[int][]string{0: {"Read(main.go)"}}}
				got := ansi.Strip(RenderToolCalls(params))
				lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
				if width >= 16 && !strings.HasSuffix(lines[0], " · 12s") {
					t.Fatalf("width %d compact %v expanded %v missing title timer: %q", width, compact, expanded, got)
				}
				for i, line := range lines {
					if ansi.StringWidth(line) > width || i > 0 && strings.Contains(line, "12s") {
						t.Fatalf("overflow or misplaced timer: %q", got)
					}
				}
			}
		}
	}
}

func TestAgentCompletedElapsedTitle(t *testing.T) {
	call := core.ToolCall{ID: "worker", Name: "Agent", Input: `{"name":"reviewer","description":"Review execution and approval consistency"}`}
	for _, width := range []int{32, 40, 80} {
		for _, state := range []struct {
			name    string
			details any
			timed   bool
		}{
			{"completed", toolresult.AgentDetails{Duration: 12 * time.Second}, true},
			{"stopped", toolresult.AgentDetails{Duration: 12 * time.Second, Stopped: true}, true},
			{"background", toolresult.AgentDetails{Duration: 12 * time.Second, TaskID: "task"}, false},
			{"unknown duration", toolresult.AgentDetails{}, false},
			{"legacy", nil, false},
		} {
			got := ansi.Strip(RenderToolCalls(ToolCallsParams{ToolCalls: []core.ToolCall{call}, Width: width, ResultMap: map[string]ToolResultData{call.ID: {ToolName: "Agent", Content: "Finding", Details: state.details}}}))
			title, _, _ := strings.Cut(got, "\n")
			if strings.HasSuffix(title, " · 12s") != state.timed || ansi.StringWidth(title) > width {
				t.Fatalf("%s width %d: %q", state.name, width, got)
			}
		}
	}
}
