package input

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// numberedLines builds n distinguishable, single-row lines.
func numberedLines(n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = "line-" + string(rune('A'+i%26)) + "-" + strings.Repeat("x", i%3)
	}
	return lines
}

func TestTranscriptViewerEnterAndCancel(t *testing.T) {
	var h TranscriptViewer
	if h.IsActive() {
		t.Fatal("a fresh viewer must not be active")
	}
	h.Enter("History · 3 messages earlier", []string{"a", "b", "c"}, 80, 24)
	if !h.IsActive() {
		t.Fatal("Enter should activate the viewer")
	}
	h.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEscape})
	if h.IsActive() {
		t.Fatal("esc should close the viewer")
	}
	if h.body.TotalLineCount() != 0 {
		t.Fatal("closing should release the rendered lines")
	}
}

// Scrolling is clamped at both ends: past the top there is nothing, and past
// the bottom the last line stays on screen rather than scrolling into blank.
func TestTranscriptViewerClampsScroll(t *testing.T) {
	var h TranscriptViewer
	lines := numberedLines(200)
	h.Enter("t", lines, 80, 24)

	h.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyUp})
	if !h.body.AtTop() {
		t.Fatalf("scrolling up from the top must stay there, offset %d", h.body.YOffset())
	}

	h.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEnd})
	h.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if !h.body.AtBottom() || h.body.PastBottom() {
		t.Fatalf("paging past the end must clamp to the last page, offset %d", h.body.YOffset())
	}
	if !strings.Contains(h.Render(), lines[len(lines)-1]) {
		t.Fatal("the last line should be visible at the bottom")
	}

	h.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyHome})
	if !h.body.AtTop() {
		t.Fatalf("home should return to the top, offset %d", h.body.YOffset())
	}
}

// A body shorter than the viewport has nothing to scroll, and the position
// label is omitted rather than reporting a meaningless range.
func TestTranscriptViewerShortBodyHasNoScrollRange(t *testing.T) {
	var h TranscriptViewer
	h.Enter("t", []string{"only"}, 80, 24)
	if strings.Contains(h.hint(), " of ") {
		t.Fatalf("a body that fits should not report a position: %q", h.hint())
	}
}

// The position label is what tells the reader the view is bounded — the rows
// above and below it are not in the terminal's scrollback, so there is nothing
// to scroll up to.
func TestTranscriptViewerReportsPositionWhenScrollable(t *testing.T) {
	var h TranscriptViewer
	h.Enter("t", numberedLines(200), 80, 24)
	if !strings.Contains(h.hint(), "1–17 of 200") {
		t.Fatalf("a scrollable body should report its position: %q", h.hint())
	}
}

// Resize implements resizableOverlay, so a terminal resize re-clamps the offset
// instead of leaving the view scrolled past a now-shorter body.
func TestTranscriptViewerResizeReclamps(t *testing.T) {
	var h TranscriptViewer
	h.Enter("t", numberedLines(200), 80, 40)
	h.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEnd})

	h.Resize(80, 12)
	if h.body.PastBottom() {
		t.Fatalf("offset %d is past the bottom after shrinking", h.body.YOffset())
	}
}

// Render keeps a stable frame — the body is padded to its full height even when
// the content is short — and shows only the lines in the window.
func TestTranscriptViewerRendersTheWindow(t *testing.T) {
	for _, n := range []int{2, 500} {
		var h TranscriptViewer
		h.Enter("History · earlier", numberedLines(n), 80, 24)

		out := h.Render()
		if rows := strings.Count(out, "\n") + 1; rows != 24-transcriptFrameMargin {
			t.Fatalf("%d lines: rendered %d rows, want height-%d = %d",
				n, rows, transcriptFrameMargin, 24-transcriptFrameMargin)
		}
		if !strings.Contains(out, "History · earlier") {
			t.Fatalf("the title is missing:\n%s", out)
		}
		if !strings.Contains(out, "line-A") || strings.Contains(out, "line-Z") {
			t.Fatalf("the body should be the visible window only:\n%s", out)
		}
	}
}
