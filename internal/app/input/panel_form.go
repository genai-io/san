// skillConfigForm edits a working Auto Skill settings snapshot and saves it
// to user or project settings. The owning panel supplies the rows and sub-views.
package input

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/setting"
)

// ConfigSavedMsg reports the Auto Skill settings written to disk so the app
// can reload them and report any overrides from another scope.
type ConfigSavedMsg struct {
	Scope          setting.Scope
	SavedAutoSkill setting.AutoSkillSettings
}

type skillConfigForm struct {
	// workspace returns the live cwd + settings service; read at Enter so a
	// project reload is picked up. Nil (tests) seeds a zero snapshot.
	workspace func() (string, *setting.Settings)

	rows []configRow

	// snap is the working buffer; Save merges it back to disk.
	snap setting.AutoSkillSettings
	// baseline captures snap as it was at Enter() time so the form can flag
	// unsaved edits in the top-right corner.
	baseline setting.AutoSkillSettings
	scope    setting.Scope

	cursor  int
	saveErr error
}

func (f *skillConfigForm) Enter() {
	f.saveErr = nil
	f.snap = setting.AutoSkillSettings{}
	if f.workspace != nil {
		if _, settings := f.workspace(); settings != nil {
			if data := settings.Snapshot(); data != nil {
				f.snap = data.AutoSkill
			}
		}
	}
	f.baseline = f.snap
	f.cursor = findEditable(f.rows, 0, +1, 0)
}

// Dirty reports whether the working snapshot diverges from the disk
// baseline — the shell uses it to pin the "● unsaved" indicator.
func (f *skillConfigForm) Dirty() bool { return f.snap != f.baseline }

// HandleKey handles one keypress. activated names the rowEntry the user
// pressed enter on ("" otherwise) so the owning panel can open the matching
// sub-view; done=true asks the shell to dismiss the popup (after Save).
func (f *skillConfigForm) HandleKey(msg tea.KeyMsg) (cmd tea.Cmd, done bool, activated string) {
	rows := f.rows
	if f.cursor >= len(rows) {
		f.cursor = findEditable(rows, 0, +1, 0)
	}
	switch msg.String() {
	case "up", "k":
		f.cursor = findEditable(rows, f.cursor-1, -1, f.cursor)
	case "down", "j":
		f.cursor = findEditable(rows, f.cursor+1, +1, f.cursor)
	case "left", "right":
		if f.scope == setting.ScopeUser {
			f.scope = setting.ScopeProject
		} else {
			f.scope = setting.ScopeUser
		}
	case "space":
		if r := rows[f.cursor]; r.kind == rowBool && r.toggle != nil {
			r.toggle(&f.snap)
		}
	case "enter":
		row := rows[f.cursor]
		switch row.kind {
		case rowBool:
			if row.toggle != nil {
				row.toggle(&f.snap)
			}
		case rowEntry:
			// The form doesn't own the sub-view; tell the panel to open it.
			return nil, false, row.entryID
		case rowSave:
			cmd, done = f.save()
			return cmd, done, ""
		}
	}
	return nil, false, ""
}

// save validates the working snapshot and persists it to the scoped settings
// file, emitting ConfigSavedMsg + done=true on success. A failed validation
// keeps the popup open (the inline error already shows why).
func (f *skillConfigForm) save() (tea.Cmd, bool) {
	if err := f.snap.Validate(); err != nil {
		return nil, false
	}
	if f.saveErr = setting.UpdateAutoSkillAt(f.snap, f.scope); f.saveErr != nil {
		return nil, false
	}
	scope := f.scope
	saved := f.snap
	return func() tea.Msg {
		return ConfigSavedMsg{Scope: scope, SavedAutoSkill: saved}
	}, true
}

// HintLine renders the bottom hint as monospace keycaps.
func (f *skillConfigForm) HintLine() string {
	return keycap("↑↓") + " navigate  " +
		keycap("space") + " toggle  " +
		keycap("enter") + " edit/save  " +
		keycap("←→") + " scope"
}

// Render draws the config-zone body. The arm's identity comes from the tab
// strip above, so there is no section header or rail here — just the scope
// control and a flat, calmly-spaced list of rows.
func (f *skillConfigForm) Render(width int) string {
	rows := f.rows
	validationErr := f.snap.Validate()

	var b strings.Builder
	b.WriteString(f.renderScopeControl())
	b.WriteString("\n\n")

	for i, row := range rows {
		switch row.kind {
		case rowSubHeader:
			// Align the label with the entry-row labels (which sit past a
			// 2-col cursor gutter) so the section headers read as one level.
			indentPad := strings.Repeat(" ", contentCol(row.indent))
			b.WriteString(indentPad + selflearnSubHeaderStyle.Render(strings.ToUpper(row.label)))
			if row.summary != nil {
				b.WriteString(selflearnMutedStyle.Render("  " + row.summary()))
			}
			if row.desc != "" {
				b.WriteString(selflearnMutedStyle.Render(row.desc))
			}
		case rowSpacer:
			// blank line
		case rowSave:
			b.WriteString(f.renderSaveRow(i, validationErr))
		case rowBool:
			b.WriteString(f.renderBoolRow(i, row))
		case rowEntry:
			b.WriteString(f.renderEntryRow(i, row, width))
		}
		b.WriteString("\n")
	}

	if validationErr == nil && f.saveErr != nil {
		b.WriteString(selflearnErrorStyle.Render("Save failed: " + f.saveErr.Error()))
		b.WriteString("\n")
	}
	if validationErr != nil {
		b.WriteString("\n")
		b.WriteString(selflearnErrorStyle.Render("⚠ " + validationErr.Error()))
		b.WriteString("\n")
	}
	return b.String()
}

// renderScopeControl displays the current Auto Skill save target.
func (f *skillConfigForm) renderScopeControl() string {
	seg := func(scope setting.Scope) string {
		if f.scope == scope {
			return selflearnScopeActiveStyle.Render(string(scope))
		}
		return selflearnScopeIdleStyle.Render(string(scope))
	}
	sep := selflearnMutedStyle.Render(" · ")
	return "Skill save scope: " + seg(setting.ScopeUser) + sep + seg(setting.ScopeProject)
}

// keycap renders a key label as a bg-filled pill so it doesn't read as
// a checkbox. The fill is the kit's neutral search-input gray so the
// keycap feels like a physical key cap, not a [ ] toggle.
func keycap(s string) string {
	return selflearnKeycapStyle.Render(" " + capitalizeKeyLabel(s) + " ")
}

// capitalizeKeyLabel title-cases each part of a key combo so hints read as
// proper key names: "ctrl+r" → "Ctrl+R", "enter" → "Enter". Arrow glyphs
// (↑↓, ←→) and other non-letters pass through unchanged.
func capitalizeKeyLabel(s string) string {
	if len([]rune(s)) == 1 {
		return s // a bare letter key: "D" would read as shift+d
	}
	parts := strings.Split(s, "+")
	for i, p := range parts {
		if p == "" {
			continue
		}
		r := []rune(p)
		r[0] = unicode.ToUpper(r[0])
		parts[i] = string(r)
	}
	return strings.Join(parts, "+")
}

// ── Row rendering ───────────────────────────────────────────────────────

// Layout columns. Every row's "content" (bracket for bool, value for int)
// starts at a column derived from its indent (indentStep cols per level);
// the cursor caret sits cursorWidth cols before that. Section headers go
// at indent 0 (col 0). Rows directly under a section: indent 1 (col 4).
// Sub-sections under those: header at indent 1, rows at indent 2 (col 8).
const (
	indentStep  = 4
	cursorWidth = 2
)

// contentCol returns the column where the row's leftmost rendered content
// (bracket / value / save button) starts, for the given indent.
func contentCol(indent int) int { return indent * indentStep }

func (f *skillConfigForm) cursorMark(i int) string {
	if i == f.cursor {
		return selflearnCursorStyle.Render("▸ ")
	}
	return strings.Repeat(" ", cursorWidth)
}

// cursorPad returns the leading whitespace + cursor caret so the next
// glyph lands exactly at contentCol(indent).
func (f *skillConfigForm) cursorPad(i, indent int) string {
	at := max(contentCol(indent)-cursorWidth, 0)
	return strings.Repeat(" ", at) + f.cursorMark(i)
}

func (f *skillConfigForm) renderBoolRow(i int, row configRow) string {
	mark := "[ ]"
	if row.boolGetter(&f.snap) {
		mark = selflearnCheckStyle.Render("[✓]")
	}
	line := f.cursorPad(i, row.indent) + mark + " " + row.label
	if row.note != "" {
		line += "  " + selflearnMutedStyle.Render(row.note)
	}
	return line
}

// renderEntryRow draws a drill-in section entry: "▸ STRATEGY  how it guides … built-in".
// The label is an uppercase muted-bold header (the same level as a sub-header),
// with a muted description and a right-aligned value hint; enter-to-open is in
// the bottom hint.
func (f *skillConfigForm) renderEntryRow(i int, row configRow, width int) string {
	left := f.cursorPad(i, row.indent) + selflearnSubHeaderStyle.Render(strings.ToUpper(row.label))
	if row.desc != "" {
		left += "  " + selflearnMutedStyle.Render(row.desc)
	}
	right := ""
	if row.summary != nil {
		right = selflearnEntrySummaryStyle.Render(row.summary())
	}
	if right == "" {
		return left
	}
	// Leave a 2-col margin before the card edge so the rail-prefixed row never
	// spills the summary onto a wrapped line.
	gap := max(width-lipgloss.Width(left)-lipgloss.Width(right)-2, 1)
	return left + strings.Repeat(" ", gap) + right
}

func (f *skillConfigForm) renderSaveRow(i int, validationErr error) string {
	style := selflearnSaveButtonStyle
	if validationErr != nil {
		style = selflearnSaveButtonDisabledStyle
	}
	btn := style.Render("Save")
	tail := selflearnMutedStyle.Render("  or " + keycap("esc") + selflearnMutedStyle.Render(" to discard"))
	return f.cursorPad(i, 1) + btn + tail
}

// ── Row kinds and layout ────────────────────────────────────────────────

type rowKind int

const (
	rowBool  rowKind = iota
	rowEntry         // opens a panel-owned sub-view
	rowSave
	rowSubHeader // sub-section title (e.g. "Learn by")
	rowSpacer    // blank line
)

// configRow is one renderable row. Fields unused by the row's kind stay zero.
type configRow struct {
	kind       rowKind
	label      string
	toggle     func(*setting.AutoSkillSettings)
	boolGetter func(*setting.AutoSkillSettings) bool
	note       string // for rowBool — optional muted inline hint after the label
	indent     int
	// entryID identifies a rowEntry to its owning panel; desc/summary render
	// the muted description and the right-aligned value hint (e.g. "built-in").
	entryID string
	desc    string
	summary func() string
}

// editable reports whether the row can hold the cursor.
func (r configRow) editable() bool {
	switch r.kind {
	case rowBool, rowEntry, rowSave:
		return true
	default:
		return false
	}
}

// findEditable walks rows from start in direction step (+1 forward,
// −1 backward) until it hits an editable row. Returns fallback when
// no editable row is found in that direction — start ≥ len(rows) for
// the first-row lookup, the cursor itself for next/prev so navigation
// past the last actionable row stays put.
func findEditable(rows []configRow, start, step, fallback int) int {
	for i := start; i >= 0 && i < len(rows); i += step {
		if rows[i].editable() {
			return i
		}
	}
	return fallback
}

// ── Styles ──────────────────────────────────────────────────────────────

// Palette: Focus is the single accent, reserved for "focus / on" — the
// cursor caret and a checked box. Everything structural is grayscale
// (Text → Muted → TextDim → Faint). Error red is the only other hue, kept for
// the rare validation message.
var (
	// Headers stay calm muted-bold caps — headers are soft signposts, not
	// shouting accents.
	selflearnSubHeaderStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted).Bold(true)

	selflearnMutedStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	selflearnErrorStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Error)

	// The one accent: cursor caret and checked box.
	selflearnCursorStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Focus).Bold(true)
	selflearnCheckStyle  = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Focus)

	// Two-segment scope control: the active segment is a neutral filled pill,
	// the inactive one a flat dim label.
	selflearnScopeActiveStyle = lipgloss.NewStyle().
					Background(kit.SearchBg).
					Foreground(kit.CurrentTheme.Text).
					Bold(true).
					Padding(0, 1)
	selflearnScopeIdleStyle = lipgloss.NewStyle().
				Foreground(kit.CurrentTheme.TextDim).
				Padding(0, 1)

	// Keycap pill — neutral gray bg + bold text so it reads as a physical key.
	selflearnKeycapStyle = lipgloss.NewStyle().
				Background(kit.SearchBg).
				Foreground(kit.CurrentTheme.Text).
				Bold(true)

	// Save button — a neutral filled pill; the cursor caret marks focus.
	// Dimmed when the snapshot fails validation.
	selflearnSaveButtonStyle = lipgloss.NewStyle().
					Background(kit.SearchBg).
					Foreground(kit.CurrentTheme.Text).
					Bold(true).
					Padding(0, 2)
	selflearnSaveButtonDisabledStyle = lipgloss.NewStyle().
						Background(kit.SearchBg).
						Foreground(kit.CurrentTheme.TextDim).
						Padding(0, 2)

	// Right-aligned value hint on a sub-view entry row ("built-in" / "custom").
	selflearnEntrySummaryStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim)
)
