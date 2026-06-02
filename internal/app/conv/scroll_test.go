package conv

import (
	"fmt"
	"strings"
	"testing"
)

// makeLines returns n lines numbered "L0".."L{n-1}".
func makeLines(n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("L%d", i)
	}
	return lines
}

func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

const (
	aboveMarker = "more lines above"
	belowMarker = "lines below"
)

func TestScrollWindow_FitsResetsOffset(t *testing.T) {
	lines := makeLines(5)
	out, off := ScrollWindow(lines, 3, 10)

	if want := strings.Join(lines, "\n"); out != want {
		t.Fatalf("content fits: got %q, want %q", out, want)
	}
	if off != 0 {
		t.Fatalf("content fits: offset = %d, want 0 (nothing to scroll)", off)
	}
	if strings.Contains(out, aboveMarker) || strings.Contains(out, belowMarker) {
		t.Fatalf("content fits: should have no indicators, got %q", out)
	}
}

func TestScrollWindow_NonPositiveMaxHeight(t *testing.T) {
	lines := makeLines(5)
	out, off := ScrollWindow(lines, 2, 0)

	if want := strings.Join(lines, "\n"); out != want {
		t.Fatalf("maxHeight<=0: got %q, want %q", out, want)
	}
	if off != 2 {
		t.Fatalf("maxHeight<=0: offset = %d, want 2 (unchanged)", off)
	}
}

func TestScrollWindow_AtBottomShowsLatestNoIndicators(t *testing.T) {
	lines := makeLines(100)
	out, off := ScrollWindow(lines, 0, 10)

	if off != 0 {
		t.Fatalf("offset = %d, want 0", off)
	}
	if got := lineCount(out); got != 10 {
		t.Fatalf("line count = %d, want 10", got)
	}
	if strings.Contains(out, aboveMarker) || strings.Contains(out, belowMarker) {
		t.Fatalf("at bottom: should have no indicators, got %q", out)
	}
	// Last line of the content must be visible at the bottom.
	if !strings.Contains(out, "L99") {
		t.Fatalf("at bottom: expected latest line L99, got %q", out)
	}
}

func TestScrollWindow_ScrolledMiddleHasBothIndicators(t *testing.T) {
	lines := makeLines(100)
	out, off := ScrollWindow(lines, 20, 10)

	if off != 20 {
		t.Fatalf("offset = %d, want 20 (not clamped)", off)
	}
	if got := lineCount(out); got > 10 {
		t.Fatalf("line count = %d, want <= 10", got)
	}
	if !strings.Contains(out, aboveMarker) {
		t.Fatalf("scrolled up: expected '↑ above' indicator, got %q", out)
	}
	if !strings.Contains(out, belowMarker) {
		t.Fatalf("scrolled up: expected '↓ below' indicator, got %q", out)
	}
}

func TestScrollWindow_ClampsOverScroll(t *testing.T) {
	lines := makeLines(100)
	// Way past the top — must clamp to maxOffset = total - maxHeight + 1.
	out, off := ScrollWindow(lines, 100_000, 10)

	if want := 100 - 10 + 1; off != want {
		t.Fatalf("over-scroll: offset = %d, want clamped %d", off, want)
	}
	if got := lineCount(out); got > 10 {
		t.Fatalf("over-scroll: line count = %d, want <= 10", got)
	}
	// At the very top the first line is visible and nothing remains above.
	if !strings.Contains(out, "L0") {
		t.Fatalf("over-scroll: expected first line L0 visible, got %q", out)
	}
	if strings.Contains(out, aboveMarker) {
		t.Fatalf("over-scroll: at top there should be no '↑ above' indicator, got %q", out)
	}
}

// TestScrollWindow_NeverExceedsMaxHeight is the core invariant: across a wide
// range of content sizes and offsets, the rendered window (content + any
// indicators) must never be taller than maxHeight, or it would push the input
// chrome off-screen.
func TestScrollWindow_NeverExceedsMaxHeight(t *testing.T) {
	for _, total := range []int{11, 12, 20, 50, 101} {
		for _, maxHeight := range []int{3, 5, 10, 25} {
			if total <= maxHeight {
				continue
			}
			lines := makeLines(total)
			for offset := 0; offset <= total+5; offset++ {
				out, clamped := ScrollWindow(lines, offset, maxHeight)
				if got := lineCount(out); got > maxHeight {
					t.Fatalf("total=%d maxHeight=%d offset=%d: line count = %d > maxHeight",
						total, maxHeight, offset, got)
				}
				if clamped < 0 || clamped > offset {
					t.Fatalf("total=%d maxHeight=%d offset=%d: clamped=%d out of range",
						total, maxHeight, offset, clamped)
				}
			}
		}
	}
}
