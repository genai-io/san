package conv

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"
)

func TestMemberLinesFitOneLineAndKeepTheNote(t *testing.T) {
	body := strings.Repeat("字段名和单位都要对齐 ", 10) + " · waits for you"
	for _, width := range []int{40, 49, 120} {
		line := renderMemberLine("◆", "From", "api", body, width, nil)
		if w := lipgloss.Width(line); w > width || strings.Contains(line, "\n") {
			t.Errorf("width %d: line is %d wide or wraps: %q", width, w, xansi.Strip(line))
		}
		if !strings.HasSuffix(xansi.Strip(line), " · waits for you") {
			t.Errorf("width %d: note cut: %q", width, xansi.Strip(line))
		}
	}
}

// Without a subject a line names only who it is from or to: nothing is cut
// out of the body.
func TestMemberLinesWithoutASubject(t *testing.T) {
	for in, want := range map[string]string{
		"From @api · waits for you": "◆ From @api · waits for you",
		"From @api":                 "◆ From @api",
		"From @api: schema changed": "◆ From @api: schema changed",
	} {
		if got := strings.TrimSpace(xansi.Strip(RenderAgentNotice(in, 80, nil))); got != want {
			t.Errorf("RenderAgentNotice(%q) = %q, want %q", in, got, want)
		}
	}
	if got := xansi.Strip(renderMemberLine("●", "To", "web", "", 80, nil)); got != "● To @web" {
		t.Errorf("To without a subject = %q", got)
	}
	msg := "<group-message>\nFrom: @api\nTo: @web\nSent: 2026-09-29 10:15\nUnattended-Turns: 1\n\nbody\n</group-message>"
	if got := agentEnvelopeSummary(msg); got != "From @api" {
		t.Errorf("resume summary without a subject = %q", got)
	}
}
