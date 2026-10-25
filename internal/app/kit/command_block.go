package kit

import (
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// RenderCommandBlock draws shell input as one inset rectangular surface. The
// outer margin stays on the terminal canvas; every command row, including
// explicit blank rows, fills the same background width. It adds no vertical
// padding and leaves the terminal's final column untouched.
func RenderCommandBlock(command string, width int) string {
	if width <= 0 {
		width = 80
	}
	available := max(1, width-1)
	margin := min(2, max(0, available-5))
	blockWidth := available - margin
	leftPad := min(2, max(0, blockWidth-5))
	promptWidth := min(2, max(0, blockWidth-leftPad-2))
	contentWidth := max(1, blockWidth-leftPad-promptWidth-1)
	outer := strings.Repeat(" ", margin)
	prefix := strings.Repeat(" ", leftPad) + ansi.Truncate("$ ", promptWidth, "")
	continuation := strings.Repeat(" ", leftPad+promptWidth)
	style := lipgloss.NewStyle().Foreground(CurrentTheme.Text).Background(CurrentTheme.CommandBg)

	var out strings.Builder
	for line := range strings.SplitSeq(strings.ReplaceAll(command, "\t", "    "), "\n") {
		for segment := range strings.SplitSeq(wrapCommandLine(line, contentWidth), "\n") {
			row := prefix + segment
			row += strings.Repeat(" ", max(0, blockWidth-ansi.StringWidth(row)))
			out.WriteString(outer + style.Render(row) + "\n")
			prefix = continuation
		}
	}
	return out.String()
}

// wrapCommandLine prefers word boundaries without dropping spaces, indentation,
// continuation backslashes, or wide characters. Every input character survives.
func wrapCommandLine(s string, width int) string {
	width = max(1, width)
	var out strings.Builder
	for ansi.StringWidth(s) > width && utf8.RuneCountInString(s) > 1 {
		row := ansi.Truncate(s, width, "")
		if i := strings.LastIndexByte(row, ' '); i > 0 && 2*ansi.StringWidth(row[:i+1]) >= width && !commandWordWiderThan(s[i+1:], width) {
			row = row[:i+1]
		} else if row == "" {
			_, size := utf8.DecodeRuneInString(s)
			row = s[:size]
		}
		out.WriteString(row + "\n")
		s = s[len(row):]
	}
	out.WriteString(s)
	return out.String()
}

func commandWordWiderThan(s string, width int) bool {
	word, _, _ := strings.Cut(s, " ")
	return ansi.StringWidth(word) > width
}
