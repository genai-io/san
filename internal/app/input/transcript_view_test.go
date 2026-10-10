package input

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// numberedLines builds n distinguishable, single-row lines.
func numberedLines(n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = "line-" + string(rune('A'+i%26)) + "-" + strings.Repeat("x", i%3)
	}
	return lines
}

func TestTranscriptToolsNavigateAndExpandIndependently(t *testing.T) {
	var h TranscriptViewer
	renders := [2]int{}
	entries := []TranscriptEntry{{Render: func(int, bool) string { return "prompt\n" }}}
	for i := range 2 {
		entries = append(entries, TranscriptEntry{Label: string(rune('A' + i)), Tool: true, Render: func(width int, expanded bool) string {
			renders[i]++
			if expanded {
				return string(rune('A'+i)) + " body\n" + strings.Repeat("output\n", 40)
			}
			return string(rune('A'+i)) + " summary\n"
		}})
	}
	h.EnterEntries(entries, 80, 24)
	if !strings.Contains(h.Render(), "tool 2/2 · B") || !strings.Contains(h.Render(), "B body") {
		t.Fatal("the latest tool should open expanded")
	}
	h.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyLeft})
	if !strings.Contains(h.Render(), "tool 1/2 · A") || !strings.Contains(ansi.Strip(h.Render()), "› A summary") {
		t.Fatal("left should select the previous tool")
	}
	h.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !h.entries[1].expanded || !h.entries[2].expanded || renders != [2]int{2, 1} {
		t.Fatal("expansion should re-render only the selected tool")
	}
	h.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEnter})
	if h.entries[1].expanded || !h.entries[2].expanded {
		t.Fatal("collapsing one result should leave the other expanded")
	}
	h.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyRight})
	h.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyRight})
	if h.selected != 1 {
		t.Fatal("tool navigation must clamp at the last tool")
	}
	h.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEscape})
	if len(h.entries) != 0 || len(h.tools) != 0 || len(h.offsets) != 0 {
		t.Fatal("close must release source callbacks and rendered caches")
	}
}

func TestTranscriptEntryResizeRerendersAtNewWidth(t *testing.T) {
	var h TranscriptViewer
	h.EnterEntries([]TranscriptEntry{{Label: "中文文件", Tool: true, Render: func(width int, expanded bool) string {
		if !expanded {
			return "summary"
		}
		return ansi.Wrap(strings.Repeat("中文 text ", 40), width-1, " ")
	}}}, 80, 24)
	h.Resize(30, 14)
	if h.body.PastBottom() {
		t.Fatal("resizing should keep the viewport within its content")
	}
	for _, line := range strings.Split(h.Render(), "\n") {
		if ansi.StringWidth(line) > 30 {
			t.Fatalf("narrow terminal has an overflowing line: %q", line)
		}
	}
	if rows := strings.Count(h.Render(), "\n") + 1; rows != 12 {
		t.Fatalf("resized viewer has %d rows, want 12", rows)
	}
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
	h.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyLeft})
	h.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyRight})
	if !strings.Contains(h.Render(), "only") {
		t.Fatal("tool navigation must leave static content intact")
	}
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
