// /history overlay: a fullscreen, read-only view of the messages a resume did
// not print. It takes lines the app layer already rendered and wrapped, so it
// draws them full-width instead of in the centered panel, which would wrap them
// again.
package input

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/kit"
)

// TranscriptViewer is the /history overlay: bubbles' viewport for scrolling,
// plus a title and key hint.
type TranscriptViewer struct {
	active bool
	width  int
	title  string
	body   viewport.Model
}

// Enter opens the viewer on the given lines, scrolled to the top.
func (h *TranscriptViewer) Enter(title string, lines []string, width, height int) {
	h.active = true
	h.title = title
	h.body = viewport.New()
	h.body.FillHeight = true
	h.body.SetContentLines(lines)
	h.Resize(width, height)
}

func (h *TranscriptViewer) IsActive() bool { return h.active }

// cancel closes the viewer and frees the rendered lines.
func (h *TranscriptViewer) cancel() {
	h.active = false
	h.body = viewport.Model{}
}

// Resize implements resizableOverlay; the viewport re-clamps its own offset.
func (h *TranscriptViewer) Resize(width, height int) {
	h.width = width
	h.body.SetWidth(width)
	h.body.SetHeight(max(1, height-transcriptChromeRows-transcriptFrameMargin))
}

func (h *TranscriptViewer) HandleKeypress(key tea.KeyMsg) tea.Cmd {
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

// transcriptChromeRows counts the non-body rows: separator, title, blank line,
// separator, hint.
const transcriptChromeRows = 5

// transcriptFrameMargin matches the height-2 other fullscreen selectors use.
const transcriptFrameMargin = 2

func (h *TranscriptViewer) Render() string {
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

// hint lists the keys and, when the body scrolls, the visible line range.
func (h *TranscriptViewer) hint() string {
	parts := []string{"↑/↓ scroll", "pgup/pgdn page", "home/end", "esc close"}
	if total := h.body.TotalLineCount(); total > h.body.Height() {
		first := h.body.YOffset() + 1
		last := min(h.body.YOffset()+h.body.Height(), total)
		parts = append(parts, fmt.Sprintf("%d–%d of %d", first, last, total))
	}
	return kit.HintLine(parts...)
}
