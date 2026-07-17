// strategyEditor is the /evolve Strategy sub-view: a chromeless textarea
// seeded with the built-in learning strategy (or the user's saved override).
// On close its value collapses back to "" when left unchanged from the
// default, so the panel keeps reading "built-in" and Dirty() doesn't flag a
// no-op edit. Full-editable: the saved text replaces the built-in guidance in
// the reviewer prompt (the permission gate still applies).
package input

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/app/kit"
)

type strategyEditor struct {
	ta    textarea.Model
	deflt string // the built-in strategy, set once at construction

	// Render memo: the measured display height is recomputed only when the
	// value, width, or row cap changed — the textarea measure-render is the
	// priciest widget draw in the popup, and render runs every frame.
	lastValue   string
	lastWidth   int
	lastMaxRows int
	lastRows    int
}

func newStrategyEditor(deflt string) strategyEditor {
	return strategyEditor{ta: newChromelessTextarea(), deflt: deflt}
}

// open seeds the editor with the current override (or the built-in default
// when empty) and focuses it.
func (e *strategyEditor) open(current string) {
	seed := current
	if seed == "" {
		seed = e.deflt
	}
	e.ta.SetValue(seed)
	e.ta.CursorEnd()
	e.ta.Focus()
}

// value returns the override to persist: "" when the text matches the built-in
// default (so the arm stays "built-in"), otherwise the edited text.
func (e *strategyEditor) value() string {
	v := strings.TrimRight(e.ta.Value(), "\n")
	if strings.TrimSpace(v) == strings.TrimSpace(e.deflt) {
		return ""
	}
	return v
}

func (e *strategyEditor) handleKey(msg tea.KeyMsg) tea.Cmd {
	var cmd tea.Cmd
	e.ta, cmd = e.ta.Update(msg)
	return cmd
}

func (e *strategyEditor) insert(s string) { e.ta.InsertString(s) }

// insertNewline adds a line break — bound to Alt/Shift+Enter so plain Enter is
// free to mean "save & back".
func (e *strategyEditor) insertNewline() { e.ta.InsertString("\n") }

// render sizes the textarea to its CONTENT (not the whole panel) so a short
// strategy doesn't leave a wall of blank space, then adds a one-line reminder
// that the edit is staged until the panel is saved. `height` is only an upper
// bound: long text is capped there and scrolls. The content height is measured
// by rendering once at the cap and trimming the textarea's trailing
// end-of-buffer rows — which matches the widget's own soft-wrapping exactly
// (LineCount ignores wrapping) — and memoized until the value or geometry
// changes, so steady-state frames render the textarea once.
func (e *strategyEditor) render(width, height int) string {
	e.ta.SetWidth(width)
	maxRows := max(3, height-2)
	if v := e.ta.Value(); v != e.lastValue || width != e.lastWidth || maxRows != e.lastMaxRows {
		e.lastValue, e.lastWidth, e.lastMaxRows = v, width, maxRows
		e.ta.SetHeight(maxRows)
		measured := trimTrailingBlankLines(e.ta.View())
		e.lastRows = min(maxRows, max(3, strings.Count(measured, "\n")+1))
	}
	e.ta.SetHeight(e.lastRows)
	return e.ta.View() + "\n\n" +
		strategyNoteStyle.Render("Replaces the built-in learning strategy — persisted when you Save the panel.")
}

// trimTrailingBlankLines drops trailing blank lines (the textarea pads its box
// to the set height with end-of-buffer rows). Those rows carry the widget's
// styling, so the ANSI escapes must be stripped before the whitespace test —
// TrimSpace alone leaves the escape bytes and never sees the row as empty.
func trimTrailingBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	n := len(lines)
	for n > 0 && strings.TrimSpace(xansi.Strip(lines[n-1])) == "" {
		n--
	}
	return strings.Join(lines[:n], "\n")
}

// strategySummary is the right-aligned value hint on the Strategy entry row.
func strategySummary(override string) string {
	if strings.TrimSpace(override) != "" {
		return "custom"
	}
	return "built-in"
}

var strategyNoteStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted).Italic(true)
