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
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/kit"
)

// HistoryViewer shows a scrollable block of pre-rendered transcript lines. The
// scrolling itself — offset, clamping at both ends, padding the body to a
// stable height — is bubbles' viewport; this type owns only the frame around it.
type HistoryViewer struct {
	active bool
	width  int
	title  string
	body   viewport.Model
}

// Enter opens the viewer on the given lines, scrolled to the top.
func (h *HistoryViewer) Enter(title string, lines []string, width, height int) {
	h.active = true
	h.title = title
	h.body = viewport.New()
	h.body.FillHeight = true
	h.body.SetContentLines(lines)
	h.Resize(width, height)
}

func (h *HistoryViewer) IsActive() bool { return h.active }

// cancel closes the viewer and releases the rendered lines, which can be the
// bulk of a long transcript.
func (h *HistoryViewer) cancel() {
	h.active = false
	h.body = viewport.Model{}
}

// Resize implements resizableOverlay: the frame caches the terminal size it was
// opened with, so a resize has to refresh it or the stale-width frame hard-wraps
// and leaves fragments behind. The viewport re-clamps its offset to the new
// body height itself.
func (h *HistoryViewer) Resize(width, height int) {
	h.width = width
	h.body.SetWidth(width)
	h.body.SetHeight(max(1, height-historyChromeRows-historyFrameMargin))
}

func (h *HistoryViewer) HandleKeypress(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "esc", "q":
		h.cancel()
	case "up", "k":
		h.body.ScrollUp(1)
	case "down", "j":
		h.body.ScrollDown(1)
	case "pgup", "ctrl+b":
		h.body.PageUp()
	case "pgdown", "ctrl+f", " ":
		h.body.PageDown()
	case "home", "g":
		h.body.GotoTop()
	case "end", "G":
		h.body.GotoBottom()
	}
	return nil
}

// historyChromeRows is the number of rendered rows that are not body: the
// separator + title, the blank line under it, and the separator + hint below.
const historyChromeRows = 5

// historyFrameMargin is the blank slack left below the frame, matching the
// height-2 that every other fullscreen selector's Wrap centers into.
const historyFrameMargin = 2

func (h *HistoryViewer) Render() string {
	if !h.active {
		return ""
	}
	separator := kit.Panel{Width: h.width}.SeparatorLine()
	return strings.Join([]string{
		separator,
		kit.SelectorTitleStyle().Render(h.title),
		"",
		h.body.View(),
		separator,
		h.hint(),
	}, "\n")
}

// hint states the keys plus where the view sits in the transcript. The position
// matters more than usual here: the rows above and below the window are not in
// the terminal's scrollback, so there is nothing to scroll up to, and without a
// position the reader cannot tell the block is bounded.
func (h *HistoryViewer) hint() string {
	parts := []string{"↑/↓ scroll", "pgup/pgdn page", "home/end", "esc close"}
	if total := h.body.TotalLineCount(); total > h.body.Height() {
		first := h.body.YOffset() + 1
		last := min(h.body.YOffset()+h.body.Height(), total)
		parts = append(parts, fmt.Sprintf("%d–%d of %d", first, last, total))
	}
	return kit.HintLine(parts...)
}
