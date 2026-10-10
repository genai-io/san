// Fullscreen transcript viewer. The app supplies the existing message/tool
// renderers; this overlay owns only scrolling, selection and expansion.
package input

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/app/kit"
)

// TranscriptEntry is one message or tool call, rendered at the viewer's width.
type TranscriptEntry struct {
	Label  string
	Tool   bool
	Render func(width int, expanded bool) string

	expanded bool
	lines    []string
}

// TranscriptViewer keeps a disposable rendered view, never a second transcript.
type TranscriptViewer struct {
	active     bool
	width      int
	title      string
	body       viewport.Model
	entries    []TranscriptEntry
	tools      []int
	offsets    []int
	selected   int
	generation uint64
}

// Enter opens the viewer on the given lines, scrolled to the top.
func (h *TranscriptViewer) Enter(title string, lines []string, width, height int) {
	h.generation++
	h.active = true
	h.title = title
	h.entries, h.tools, h.offsets = nil, nil, nil
	h.body = viewport.New()
	h.body.FillHeight = true
	h.body.SetContentLines(lines)
	h.Resize(width, height)
}

// EnterEntries opens at the most recent tool result, expanded for inspection.
func (h *TranscriptViewer) EnterEntries(entries []TranscriptEntry, width, height int) {
	h.Enter("History", nil, width, height)
	h.entries = entries
	for i, entry := range entries {
		if entry.Tool {
			h.tools = append(h.tools, i)
		}
	}
	h.selected = max(0, len(h.tools)-1)
	if len(h.tools) > 0 {
		h.entries[h.tools[h.selected]].expanded = true
	}
	h.renderEntries()
	if len(h.tools) > 0 {
		h.gotoSelected()
	} else {
		h.body.GotoBottom()
	}
}

// Generation lets the app discard a load that completed after close/reopen.
func (h *TranscriptViewer) Generation() uint64 { return h.generation }

func (h *TranscriptViewer) IsActive() bool { return h.active }

// cancel closes the viewer and frees the rendered lines.
func (h *TranscriptViewer) cancel() {
	h.active = false
	h.generation++
	h.body = viewport.Model{}
	h.entries, h.tools, h.offsets = nil, nil, nil
}

// Resize implements resizableOverlay; the viewport re-clamps its own offset.
func (h *TranscriptViewer) Resize(width, height int) {
	oldWidth := h.width
	h.width = width
	h.body.SetWidth(width)
	h.body.SetHeight(max(1, height-transcriptChromeRows-transcriptFrameMargin))
	if oldWidth != width && len(h.entries) > 0 {
		h.renderEntries()
		h.gotoSelected()
	}
}

func (h *TranscriptViewer) renderEntry(i int) {
	entry := &h.entries[i]
	text := strings.TrimRight(entry.Render(max(1, h.width-2), entry.expanded), "\n")
	entry.lines = nil
	if text != "" {
		entry.lines = strings.Split(text, "\n")
	}
}

func (h *TranscriptViewer) renderEntries() {
	for i := range h.entries {
		h.renderEntry(i)
	}
	h.rebuildLines()
}

func (h *TranscriptViewer) rebuildLines() {
	var lines []string
	h.offsets = make([]int, len(h.entries))
	for i, entry := range h.entries {
		h.offsets[i] = len(lines)
		for j, line := range entry.lines {
			prefix := "  "
			if len(h.tools) > 0 && i == h.tools[h.selected] && j == 0 {
				prefix = "› "
			}
			lines = append(lines, prefix+line)
		}
	}
	h.body.SetContentLines(lines)
}

func (h *TranscriptViewer) gotoSelected() {
	if len(h.tools) > 0 {
		h.body.SetYOffset(h.offsets[h.tools[h.selected]])
	}
}

func (h *TranscriptViewer) toggleSelected() {
	if len(h.tools) == 0 {
		return
	}
	i := h.tools[h.selected]
	h.entries[i].expanded = !h.entries[i].expanded
	h.renderEntry(i)
	h.rebuildLines()
	h.gotoSelected()
}

func (h *TranscriptViewer) HandleKeypress(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "esc", "q", "ctrl+c":
		h.cancel()
	case "left", "p", "shift+tab":
		if len(h.tools) == 0 {
			return nil
		}
		h.selected = max(0, h.selected-1)
		h.rebuildLines()
		h.gotoSelected()
	case "right", "n", "tab":
		if len(h.tools) == 0 {
			return nil
		}
		h.selected = max(0, min(len(h.tools)-1, h.selected+1))
		h.rebuildLines()
		h.gotoSelected()
	case "enter":
		h.toggleSelected()
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
	separator := kit.DimStyle().Render(strings.Repeat("─", max(1, h.width-1)))
	title := h.title
	if len(h.tools) > 0 {
		entry := h.entries[h.tools[h.selected]]
		title = fmt.Sprintf("History · tool %d/%d · %s", h.selected+1, len(h.tools), entry.Label)
	}
	return strings.Join([]string{
		separator,
		kit.SelectorTitleStyle().Render(ansi.Truncate(title, max(1, h.width-1), "…")),
		"",
		h.body.View(),
		separator,
		h.hint(),
	}, "\n")
}

// hint lists the keys and, when the body scrolls, the visible line range.
func (h *TranscriptViewer) hint() string {
	parts := []string{"↑/↓ scroll", "pgup/dn", "home/end", "esc close"}
	if len(h.tools) > 0 {
		action := "expand"
		if h.entries[h.tools[h.selected]].expanded {
			action = "collapse"
		}
		parts = []string{"←/→ tool", "enter " + action, "↑/↓ scroll", "pgup/dn", "esc close"}
	}
	if total := h.body.TotalLineCount(); total > h.body.Height() {
		first := h.body.YOffset() + 1
		last := min(h.body.YOffset()+h.body.Height(), total)
		parts = append(parts, fmt.Sprintf("%d–%d of %d", first, last, total))
	}
	return ansi.Truncate(kit.HintLine(parts...), max(1, h.width-1), "…")
}
