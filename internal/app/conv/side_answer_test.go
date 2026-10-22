package conv

import (
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
)

func TestRenderSideAnswerWrapsWithoutPadding(t *testing.T) {
	out := RenderSideAnswer("/btw "+strings.Repeat("问题", 20)+"\n\nanswer", 30, nil)
	lines := strings.Split(strings.TrimSuffix(xansi.Strip(out), "\n"), "\n")
	if len(lines) < 3 || !strings.HasPrefix(lines[0], "◇ btw 问题") || !strings.HasPrefix(lines[1], "      问题") {
		t.Fatalf("question must wrap under its first row:\n%s", strings.Join(lines, "\n"))
	}
	for _, l := range lines {
		if l != strings.TrimRight(l, " ") {
			t.Fatalf("row padded with spaces rewraps when scrollback narrows: %q", l)
		}
	}
	if lines[len(lines)-1] != "  answer" {
		t.Fatalf("answer must sit under the reply gutter: %q", lines[len(lines)-1])
	}
}
