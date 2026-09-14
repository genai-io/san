package conv

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"
)

// A carriage return left in the line lands inside the "┊" gutter, and the
// terminal restarts the row at column 0. git rebase is where people see it.
func TestNestedToolBodyDropsOverwrittenProgress(t *testing.T) {
	body := renderNestedToolBody("Rebasing (1/1)\rAuto-merging f.txt\nCONFLICT (content): Merge conflict", 80)

	if strings.Contains(body, "\r") {
		t.Fatalf("a carriage return reached the frame: %q", body)
	}
	if strings.Contains(body, "Rebasing (1/1)") {
		t.Errorf("overwritten progress was rendered: %q", body)
	}
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		if !strings.HasPrefix(xansi.Strip(line), "  ┊ ") && strings.TrimSpace(xansi.Strip(line)) != "" {
			t.Errorf("line escaped the gutter: %q", line)
		}
	}
	for _, want := range []string{"Auto-merging f.txt", "CONFLICT (content): Merge conflict"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lost %q: %q", want, body)
		}
	}
}

// CRLF is a line ending, not an overwrite: the text before it is what was shown.
func TestNestedToolBodyKeepsCRLFContent(t *testing.T) {
	body := renderNestedToolBody("first line\r\nsecond line\r", 80)
	for _, want := range []string{"first line", "second line"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lost %q: %q", want, body)
		}
	}
}

// A row wider than the terminal is wrapped here, gutter and all: left to the
// terminal, the continuation restarts at column 0 outside the "┊" connector.
func TestNestedToolBodyWrapsLongRowsInsideGutter(t *testing.T) {
	const width = 40
	long := "test\tpending\t0\thttps://github.com/genai-io/san/actions/runs/34793367902/job/103821690303"
	body := renderNestedToolBody(long, width)

	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("long row was not wrapped: %q", body)
	}
	var joined strings.Builder
	for _, line := range lines {
		plain := xansi.Strip(line)
		if !strings.HasPrefix(plain, "  ┊ ") {
			t.Errorf("continuation escaped the gutter: %q", plain)
		}
		if w := lipgloss.Width(plain); w > width {
			t.Errorf("row is %d cells wide, terminal is %d: %q", w, width, plain)
		}
		joined.WriteString(strings.TrimPrefix(plain, "  ┊ "))
	}
	// A break eats the whitespace it lands on, so compare everything else.
	if got, want := strings.Join(strings.Fields(joined.String()), ""), strings.Join(strings.Fields(long), ""); got != want {
		t.Errorf("wrapped rows lost content:\n got %q\nwant %q", got, want)
	}
}
