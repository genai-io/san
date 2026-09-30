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
