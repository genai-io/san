// /history overlay: a fullscreen, read-only, scrollable view of the messages a
// resumed session left out of native scrollback.
//
// It is handed pre-rendered lines rather than messages on purpose. Rendering a
// transcript needs the conversation render context and the terminal's markdown
// renderer, both of which belong to the app layer; the viewer only scrolls and
// frames, which is what this package owns. The lines arrive already wrapped to
// the terminal width, so the body is drawn full-width rather than inside the
// shared centered panel — a box narrower than the lines would hard-wrap them a
// second time and the scroll offsets would no longer match the rows on screen.
package input

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/kit"
)

// HistoryViewer shows a scrollable block of pre-rendered transcript lines.
type HistoryViewer struct {
	active bool
	width  int
	height int

	title string
	lines []string
	top   int // index of the first visible line
}

// Enter opens the viewer on the given lines. An empty body still opens: the
// overlay then says so, which is a better answer than a key that appears to do
// nothing.
func (h *HistoryViewer) Enter(title string, lines []string, width, height int) {
	h.active = true
	h.width = width
	h.height = height
	h.title = title
	h.lines = lines
	h.top = 0
}

func (h *HistoryViewer) IsActive() bool { return h.active }

func (h *HistoryViewer) cancel() {
	h.active = false
	h.lines = nil
}

// Resize implements resizableOverlay: the frame caches the terminal size it was
// opened with, so a resize has to refresh it or the stale-width frame hard-wraps
// and leaves fragments behind.
func (h *HistoryViewer) Resize(width, height int) {
	h.width = width
	h.height = height
	h.clampTop()
}

func (h *HistoryViewer) HandleKeypress(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "esc", "q":
		h.cancel()
	case "up", "k":
		h.top--
	case "down", "j":
		h.top++
	case "pgup", "ctrl+b":
		h.top -= h.bodyRows()
	case "pgdown", "ctrl+f", " ":
		h.top += h.bodyRows()
	case "home", "g":
		h.top = 0
	case "end", "G":
		h.top = len(h.lines)
	}
	h.clampTop()
	return nil
}

// historyChromeRows is the number of rendered rows that are not body: the
// separator + title, the blank line under it, and the separator + hint below.
const historyChromeRows = 5

// historyFrameMargin is the blank slack left below the frame, matching the
// height-2 that every other fullscreen selector's Wrap centers into.
const historyFrameMargin = 2

// bodyRows is how many lines fit between the header and the hint.
func (h *HistoryViewer) bodyRows() int {
	return max(1, h.height-historyChromeRows-historyFrameMargin)
}

// maxTop is the largest offset that still fills the body — scrolling past it
// would leave blank rows under the last line.
func (h *HistoryViewer) maxTop() int { return max(0, len(h.lines)-h.bodyRows()) }

func (h *HistoryViewer) clampTop() {
	h.top = min(max(h.top, 0), h.maxTop())
}

// visible returns the body slice for the current offset, padded to a full body
// so the frame keeps a stable height while scrolling.
func (h *HistoryViewer) visible() []string {
	rows := h.bodyRows()
	out := make([]string, 0, rows)
	for i := h.top; i < len(h.lines) && len(out) < rows; i++ {
		out = append(out, h.lines[i])
	}
	for len(out) < rows {
		out = append(out, "")
	}
	return out
}

func (h *HistoryViewer) Render() string {
	if !h.active {
		return ""
	}
	panel := kit.Panel{Width: h.width, Height: h.height}
	dim := kit.DimStyle()

	var sb strings.Builder
	sb.WriteString(panel.SeparatorLine())
	sb.WriteString("\n")
	sb.WriteString(kit.SelectorTitleStyle().Render(h.title))
	sb.WriteString("\n\n")
	sb.WriteString(strings.Join(h.visible(), "\n"))
	sb.WriteString("\n")
	sb.WriteString(panel.SeparatorLine())
	sb.WriteString("\n")
	sb.WriteString(dim.Render(h.hint()))
	return sb.String()
}

// hint states the keys plus where the view sits in the transcript. The position
// matters more than usual here: the rows above and below the window are not in
// the terminal's scrollback, so there is nothing to scroll up to, and without a
// position the reader cannot tell the block is bounded.
func (h *HistoryViewer) hint() string {
	parts := []string{"↑/↓ scroll", "pgup/pgdn page", "home/end", "esc close"}
	if len(h.lines) > h.bodyRows() {
		parts = append(parts, positionLabel(h.top+1, min(h.top+h.bodyRows(), len(h.lines)), len(h.lines)))
	}
	return strings.Join(parts, " · ")
}

// positionLabel renders "12–34 of 512" for the scroll hint.
func positionLabel(from, to, total int) string {
	return strconv.Itoa(from) + "–" + strconv.Itoa(to) + " of " + strconv.Itoa(total)
}
