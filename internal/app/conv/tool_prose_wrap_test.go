package conv

import (
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/app/kit"
)

func TestConversationToolProseWrap(t *testing.T) {
	const prose = "Review boundary behavior carefully"
	tests := []struct {
		name  string
		width int
		out   string
		want  string
	}{
		{"agent heading", kit.ToolIconWidth + 17, renderAgentToolLine("Worker: "+prose, kit.ToolIconWidth+17, "●", ""), "● Worker: " + prose},
		{"agent prompt", 19, formatAgentDefinition(agentInput{Valid: true, Prompt: prose}, 19), "⎿ Prompt: " + prose},
		{"activity preview", 17, renderAgentActivityPreview([]string{prose}, 17, 4), prose},
		{"ask user", 19, renderAskUserCall(`{"questions":[{"question":"Review boundary behavior carefully"},{"question":"Choose another option"}]}`, "界>", 19), "界> Q1: " + prose + " Q2: Choose another option"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plain := xansi.Strip(tt.out)
			for row := range strings.SplitSeq(plain, "\n") {
				if w := xansi.StringWidth(row); w > tt.width {
					t.Errorf("row width %d exceeds %d: %q", w, tt.width, row)
				}
			}
			if got := strings.Join(strings.Fields(plain), " "); got != tt.want {
				t.Errorf("rendered text = %q, want %q (no split words or omissions)", got, tt.want)
			}
		})
	}
}

func TestConversationToolProseWrapLongPromptToken(t *testing.T) {
	const token = "abcdefghijklmnopqrstuvwxyz"
	const width = 15
	out := xansi.Strip(formatAgentDefinition(agentInput{Valid: true, Prompt: token}, width))
	_, body, _ := strings.Cut(out, "\n")
	indent := strings.Repeat(" ", toolResultExpandedStyle.GetPaddingLeft()) + agentContentIndent
	var got strings.Builder
	for row := range strings.SplitSeq(strings.TrimSuffix(body, "\n"), "\n") {
		if !strings.HasPrefix(row, indent) || xansi.StringWidth(row) > width {
			t.Errorf("invalid prompt row at width %d: %q", width, row)
		}
		got.WriteString(strings.TrimPrefix(row, indent))
	}
	if got.String() != token {
		t.Errorf("wrapped token = %q, want %q", got.String(), token)
	}
}

func TestAskUserCallStylesMarker(t *testing.T) {
	const icon = "●"
	out := renderAskUserCall(`{"question":"Choose one"}`, icon, 40)
	if !strings.HasPrefix(out, questionCallStyle.Render(icon+" ")) {
		t.Fatalf("marker lost the question style: %q", out)
	}
}

func TestAgentPromptWithUnspecifiedWidthDoesNotWrap(t *testing.T) {
	long := strings.Repeat("instructions ", 10) + "中文SanRenderer"
	agent := agentInput{Valid: true, Prompt: long + "\nSecond line"}
	for _, width := range []int{0, -1} {
		out := xansi.Strip(formatAgentDefinition(agent, width))
		lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
		if len(lines) != 3 || strings.TrimSpace(lines[1]) != long || strings.TrimSpace(lines[2]) != "Second line" {
			t.Errorf("width %d should preserve original prompt lines: %q", width, out)
		}
	}
}
