package conv

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool/toolresult"
)

func TestAgentConfiguredActivityTail(t *testing.T) {
	activity := []string{"Read(old.go)", "Model: private", "Read(one.go)", "Read(two.go)", "Read(three.go)"}
	got := ansi.Strip(renderAgentActivityPreview(activity, 60, 3))
	if got != "  Read(one.go)\n  Read(two.go)\n  Read(three.go)\n" {
		t.Fatalf("unexpected activity tail: %q", got)
	}
	if got := renderAgentActivityPreview(activity, 60, 0); got != "" {
		t.Fatalf("disabled preview: %q", got)
	}
	got = ansi.Strip(renderAgentActivityPreview([]string{strings.Repeat("x", 80)}, 20, 3))
	if len(strings.Split(strings.TrimSuffix(got, "\n"), "\n")) != 3 {
		t.Fatalf("wrapped tail exceeds budget: %q", got)
	}
}

func TestAgentConfiguredResultBudget(t *testing.T) {
	for _, budget := range []int{0, 1, 3, 5} {
		data := ToolResultData{ToolName: "Agent", Content: "one\n\ntwo\n\nthree\n\nfour\n\nfive\n\nsix", Width: 60, AgentPreviewLines: budget, Details: toolresult.AgentDetails{}}
		got := ansi.Strip(renderTaskResultInline(data, nil))
		want := budget
		if budget == 0 {
			want = 1
			if !strings.Contains(got, "Finished") {
				t.Fatalf("missing completion: %q", got)
			}
		}
		if n := strings.Count(got, "\n"); n != want {
			t.Fatalf("budget %d: got %d rows: %q", budget, n, got)
		}
		data.Expanded = true
		if got := ansi.Strip(renderTaskResultInline(data, nil)); !strings.Contains(got, "six") {
			t.Fatalf("expanded result lost output: %q", got)
		}
	}
}

func TestAgentActivityPreviewKeepsMultipleRows(t *testing.T) {
	call := core.ToolCall{ID: "worker", Name: "Agent", Input: `{}`}
	got := ansi.Strip(RenderToolCalls(ToolCallsParams{ToolCalls: []core.ToolCall{call}, PendingCalls: []core.ToolCall{call}, CurrentIdx: 0, ToolStartedAt: map[string]time.Time{"worker": time.Now()}, TaskActivity: map[int][]string{0: {"Read(one.go)", "Read(two.go)", "Read(three.go)"}}, Width: 60, AgentPreviewLines: 3}))
	var normalized strings.Builder
	for _, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
		normalized.WriteString(strings.TrimRight(line, " ") + "\n")
	}
	got = normalized.String()
	for _, line := range []string{"  Read(one.go)\n", "  Read(two.go)\n", "  Read(three.go)\n"} {
		if !strings.Contains(got, line) {
			t.Fatalf("lost activity row %q: %q", line, got)
		}
	}
}
