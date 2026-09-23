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

// Opening on an empty body is a deliberate no-op rather than an empty frame:
// the command that opens it reports "nothing to show" instead.
func TestHistoryViewerEnterAndCancel(t *testing.T) {
	var h HistoryViewer
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
	if h.lines != nil {
		t.Fatal("closing should release the rendered lines")
	}
}

// The body fills its rows exactly, so the frame height does not wobble as the
// reader scrolls: rows the content does not cover are blank, not missing.
func TestHistoryViewerFillsTheBody(t *testing.T) {
	var h HistoryViewer
	h.Enter("t", []string{"a", "b"}, 80, 24)

	body := h.visible()
	if len(body) != h.bodyRows() {
		t.Fatalf("visible rows = %d, want a full body of %d", len(body), h.bodyRows())
	}
	if body[0] != "a" || body[1] != "b" {
		t.Fatalf("body should lead with the content, got %q", body[:2])
	}
	for i := 2; i < len(body); i++ {
		if body[i] != "" {
			t.Fatalf("row %d should be blank padding, got %q", i, body[i])
		}
	}
}

// Scrolling is clamped at both ends: past the top there is nothing, and past
// the bottom the last line stays on screen rather than scrolling into blank.
func TestHistoryViewerClampsScroll(t *testing.T) {
	var h HistoryViewer
	lines := numberedLines(200)
	h.Enter("t", lines, 80, 24)

	h.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyUp})
	h.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyHome})
	if h.top != 0 {
		t.Fatalf("top = %d, want 0", h.top)
	}

	h.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEnd})
	if h.top != h.maxTop() {
		t.Fatalf("end should land on maxTop %d, got %d", h.maxTop(), h.top)
	}
	body := h.visible()
	if body[len(body)-1] != lines[len(lines)-1] {
		t.Fatalf("the last line should be visible at the bottom, got %q", body[len(body)-1])
	}

	h.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if h.top != h.maxTop() {
		t.Fatalf("paging past the end must clamp to maxTop, got %d", h.top)
	}
}

// A body shorter than the viewport has nothing to scroll, and the position
// label is omitted rather than reporting a meaningless range.
func TestHistoryViewerShortBodyHasNoScrollRange(t *testing.T) {
	var h HistoryViewer
	h.Enter("t", []string{"only"}, 80, 24)
	if h.maxTop() != 0 {
		t.Fatalf("maxTop = %d, want 0 for a body that fits", h.maxTop())
	}
	if strings.Contains(h.hint(), " of ") {
		t.Fatalf("a body that fits should not report a position: %q", h.hint())
	}
}

// The position label is what tells the reader the view is bounded — the rows
// above and below it are not in the terminal's scrollback, so there is nothing
// to scroll up to.
func TestHistoryViewerReportsPositionWhenScrollable(t *testing.T) {
	var h HistoryViewer
	h.Enter("t", numberedLines(200), 80, 24)
	if !strings.Contains(h.hint(), " of 200") {
		t.Fatalf("a scrollable body should report its position: %q", h.hint())
	}
}

// Resize implements resizableOverlay, so a terminal resize re-clamps the offset
// instead of leaving the view scrolled past a now-shorter body.
func TestHistoryViewerResizeReclamps(t *testing.T) {
	var h HistoryViewer
	h.Enter("t", numberedLines(200), 80, 40)
	h.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEnd})

	h.Resize(80, 12)
	if h.top > h.maxTop() {
		t.Fatalf("top = %d after shrinking, want <= maxTop %d", h.top, h.maxTop())
	}
}

// Render draws only the body rows plus a stable frame, and shows the lines in
// the window rather than the whole transcript.
func TestHistoryViewerRendersTheWindow(t *testing.T) {
	var h HistoryViewer
	h.Enter("History · 500 messages earlier", numberedLines(500), 80, 24)

	out := h.Render()
	rows := strings.Split(out, "\n")
	if len(rows) != historyChromeRows+h.bodyRows() {
		t.Fatalf("rendered %d rows, want %d", len(rows), historyChromeRows+h.bodyRows())
	}
	if len(rows) != h.height-historyFrameMargin {
		t.Fatalf("rendered %d rows, want height-%d = %d",
			len(rows), historyFrameMargin, h.height-historyFrameMargin)
	}
	if !strings.Contains(out, "History · 500 messages earlier") {
		t.Fatalf("the title is missing:\n%s", out)
	}
	// The window shows the head of the transcript, not all 500 lines.
	if !strings.Contains(out, "line-A") || strings.Contains(out, "line-Z") {
		t.Fatalf("the body should be the visible window only:\n%s", out)
	}
}
