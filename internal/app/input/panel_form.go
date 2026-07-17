// selfLearnForm is the config-zone form of the /evolve panel. It edits a
// working setting.SelfLearnSettings snapshot against an on-disk baseline,
// validates the §3.1 invariants inline, and writes to either the user-level
// or project-level settings file on Save.
//
// The owning panel supplies its row list via rowsFn; the snap, scope
// selector, cursor/edit machinery, and Save action live here. The panel
// renders additional zones (the Strategy editor, the Learned drill-in)
// around the form by composing rather than extending it.
package input

import (
	"strconv"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/setting"
)

// ConfigSavedMsg is emitted on a successful self-learning Save so the app can
// show a transient confirmation. SavedSelfLearn carries the snapshot that was
// just written so the consumer can compare it against the post-Reload
// effective state and detect cross-level overrides (settings merger ORs
// Enabled across user+project).
type ConfigSavedMsg struct {
	Scope          string
	SavedSelfLearn setting.SelfLearnSettings
}

type selfLearnForm struct {
	// workspace returns the live cwd + settings service; read at Enter so a
	// project reload is picked up. Nil (tests) seeds a zero snapshot.
	workspace func() (string, *setting.Settings)

	// rowsFn returns the panel-specific rows (skill rows or memory rows).
	rowsFn func() []configRow

	// snap is the working buffer; Save merges it back to disk.
	snap setting.SelfLearnSettings
	// baseline captures snap as it was at Enter() time so the form can flag
	// unsaved edits in the top-right corner.
	baseline setting.SelfLearnSettings
	scope    string // "user" | "project"

	cursor        int
	editing       bool
	editingBuffer string
}

func (f *selfLearnForm) Enter() {
	f.editing = false
	f.editingBuffer = ""
	f.snap = setting.SelfLearnSettings{}
	if f.workspace != nil {
		if _, settings := f.workspace(); settings != nil {
			if data := settings.Snapshot(); data != nil {
				f.snap = data.SelfLearn
			}
		}
	}
	f.baseline = f.snap
	f.cursor = findEditable(f.rowsFn(), 0, +1, 0)
}

// Dirty reports whether the working snapshot diverges from the disk
// baseline — the shell uses it to pin the "● unsaved" indicator.
func (f *selfLearnForm) Dirty() bool { return f.snap != f.baseline }

// HandleKey handles one keypress. activated names the rowEntry the user
// pressed enter on ("" otherwise) so the owning panel can open the matching
// sub-view; done=true asks the shell to dismiss the popup (after Save).
func (f *selfLearnForm) HandleKey(msg tea.KeyMsg) (cmd tea.Cmd, done bool, activated string) {
	if f.editing {
		return f.handleEditingKey(msg), false, ""
	}
	rows := f.rowsFn()
	if f.cursor >= len(rows) {
		f.cursor = findEditable(rows, 0, +1, 0)
	}
	switch msg.String() {
	case "up", "k":
		f.cursor = findEditable(rows, f.cursor-1, -1, f.cursor)
	case "down", "j":
		f.cursor = findEditable(rows, f.cursor+1, +1, f.cursor)
	case "left", "right":
		// ←→ flips the save target; Tab is reserved by the shell for switching
		// the skills ↔ memory tab.
		if f.scope == "user" {
			f.scope = "project"
		} else {
			f.scope = "user"
		}
	case "space":
		// Space toggles the checkbox on a plain bool row or the checkbox half
		// of a bool+int row.
		if r := rows[f.cursor]; (r.kind == rowBool || r.kind == rowBoolInt) && r.toggle != nil {
			r.toggle(&f.snap)
		}
	case "enter":
		row := rows[f.cursor]
		switch row.kind {
		case rowBool:
			if row.toggle != nil {
				row.toggle(&f.snap)
			}
		case rowInt, rowBoolInt:
			// Enter edits the number; Space toggles the checkbox (bool+int).
			f.editing = true
			f.editingBuffer = strconv.Itoa(row.intGetter(&f.snap))
		case rowText:
			f.editing = true
			f.editingBuffer = row.strGetter(&f.snap)
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
func (f *selfLearnForm) save() (tea.Cmd, bool) {
	if err := f.snap.Validate(); err != nil {
		return nil, false
	}
	userLevel := f.scope == "user"
	if err := setting.UpdateSelfLearnAt(f.snap, userLevel); err != nil {
		return nil, false
	}
	scope := f.scope
	saved := f.snap
	return func() tea.Msg {
		return ConfigSavedMsg{Scope: scope, SavedSelfLearn: saved}
	}, true
}

func (f *selfLearnForm) handleEditingKey(msg tea.KeyMsg) tea.Cmd {
	row := f.rowsFn()[f.cursor]
	switch msg.String() {
	case "esc":
		f.editing = false
		f.editingBuffer = ""
	case "enter":
		f.commitEdit(row)
		f.editing = false
		f.editingBuffer = ""
	case "backspace":
		if n := len(f.editingBuffer); n > 0 {
			// Trim a whole final rune so multi-byte paths delete cleanly.
			f.editingBuffer = trimLastRune(f.editingBuffer)
		}
	default:
		f.appendEditText(row, msg.Key().Text)
	}
	return nil
}

// commitEdit writes the edit buffer back to the snapshot: clamped int for
// rowInt, trimmed free text for rowText.
func (f *selfLearnForm) commitEdit(row configRow) {
	switch row.kind {
	case rowInt, rowBoolInt:
		if v, err := strconv.Atoi(f.editingBuffer); err == nil {
			v = min(max(v, row.intMin), row.intMax)
			row.intSetter(&f.snap, v)
		}
	case rowText:
		row.strSetter(&f.snap, strings.TrimSpace(f.editingBuffer))
	}
}

// appendEditText appends typed input: digits only (capped at 4) for the numeric
// rows, any printable text (capped) for rowText.
func (f *selfLearnForm) appendEditText(row configRow, t string) {
	switch row.kind {
	case rowInt, rowBoolInt:
		if len(t) == 1 && t[0] >= '0' && t[0] <= '9' && len(f.editingBuffer) < 4 {
			f.editingBuffer += t
		}
	case rowText:
		if t != "" && len(f.editingBuffer) < 512 {
			f.editingBuffer += t
		}
	}
}

// trimLastRune drops the final UTF-8 rune of s.
func trimLastRune(s string) string {
	r := []rune(s)
	return string(r[:len(r)-1])
}

// HintLine renders the bottom hint as monospace keycaps.
func (f *selfLearnForm) HintLine() string {
	return keycap("↑↓") + " navigate  " +
		keycap("space") + " toggle  " +
		keycap("enter") + " edit/save  " +
		keycap("←→") + " scope"
}

// Render draws the config-zone body. The arm's identity comes from the tab
// strip above, so there is no section header or rail here — just the scope
// control and a flat, calmly-spaced list of rows.
func (f *selfLearnForm) Render(width int) string {
	rows := f.rowsFn()
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
			b.WriteString("\n") // breathing room under the sub-header
		case rowSpacer:
			// blank line
		case rowSave:
			b.WriteString(f.renderSaveRow(i, validationErr))
		case rowBool:
			b.WriteString(f.renderBoolRow(i, row, width))
		case rowInt:
			b.WriteString(f.renderIntRow(i, row, width))
		case rowBoolInt:
			b.WriteString(f.renderBoolIntRow(i, row))
		case rowText:
			b.WriteString(f.renderTextRow(i, row, width))
		case rowEntry:
			b.WriteString(f.renderEntryRow(i, row, width))
		}
		b.WriteString("\n")
	}

	if validationErr != nil {
		b.WriteString("\n")
		b.WriteString(selflearnErrorStyle.Render("⚠ " + validationErr.Error()))
		b.WriteString("\n")
	}
	return b.String()
}

// renderScopeControl is a two-segment selector with the active segment
// as a filled pill. The "scope" label is dropped — the two visible
// segments (user / project) are self-explanatory.
func (f *selfLearnForm) renderScopeControl() string {
	seg := func(name string) string {
		if f.scope == name {
			return selflearnScopeActiveStyle.Render(name)
		}
		return selflearnScopeIdleStyle.Render(name)
	}
	sep := selflearnMutedStyle.Render(" · ")
	return seg("user") + sep + seg("project")
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

func (f *selfLearnForm) cursorMark(i int) string {
	if i == f.cursor {
		return selflearnCursorStyle.Render("▸ ")
	}
	return strings.Repeat(" ", cursorWidth)
}

// cursorPad returns the leading whitespace + cursor caret so the next
// glyph lands exactly at contentCol(indent).
func (f *selfLearnForm) cursorPad(i, indent int) string {
	at := max(contentCol(indent)-cursorWidth, 0)
	return strings.Repeat(" ", at) + f.cursorMark(i)
}

func (f *selfLearnForm) renderBoolRow(i int, row configRow, _ int) string {
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

// renderBoolIntRow draws a checkbox with an inline editable cadence on one line,
// e.g. "[✓] Create new skills   every (10) iterations". Space toggles the box,
// Enter edits the number. The cadence is dimmed while the checkbox is off.
func (f *selfLearnForm) renderBoolIntRow(i int, row configRow) string {
	on := row.boolGetter(&f.snap)
	mark := "[ ]"
	if on {
		mark = selflearnCheckStyle.Render("[✓]")
	}
	value := strconv.Itoa(row.intGetter(&f.snap))
	if f.editing && i == f.cursor {
		value = f.editingBuffer + "_"
	}
	cadence := selflearnMutedStyle.Render(row.intLead+" ") + valueChip(value)
	if row.unit != "" {
		cadence += " " + selflearnMutedStyle.Render(row.unit)
	}
	if !on {
		cadence = selflearnFaintStyle.Render(row.intLead + " (" + value + ") " + row.unit)
	}
	return f.cursorPad(i, row.indent) + mark + " " + row.label + "   " + cadence
}

// renderEntryRow draws a drill-in section entry: "▸ STRATEGY  how it guides … built-in".
// The label is an uppercase muted-bold header (the same level as a sub-header),
// with a muted description and a right-aligned value hint; enter-to-open is in
// the bottom hint.
func (f *selfLearnForm) renderEntryRow(i int, row configRow, width int) string {
	left := f.cursorPad(i, row.indent) + selflearnSubHeaderStyle.Render(strings.ToUpper(row.label))
	if row.desc != "" {
		left += "  " + selflearnMutedStyle.Render(row.desc)
	}
	right := ""
	if row.summary != nil {
		right = selflearnEntrySummaryStyle.Render(row.summary(&f.snap))
	}
	if right == "" {
		return left
	}
	// Leave a 2-col margin before the card edge so the rail-prefixed row never
	// spills the summary onto a wrapped line.
	gap := max(width-lipgloss.Width(left)-lipgloss.Width(right)-2, 1)
	return left + strings.Repeat(" ", gap) + right
}

func (f *selfLearnForm) renderIntRow(i int, row configRow, _ int) string {
	value := strconv.Itoa(row.intGetter(&f.snap))
	if f.editing && i == f.cursor {
		value = f.editingBuffer + "_"
	}
	// Label-first phrase: "Run every (10) user turns" reads as a sentence.
	// The label sits one bracket-width past where a bool row's "[" goes,
	// so labels align with bool-row labels at the same indent.
	labelStart := contentCol(row.indent) + 4 // 4 = bracket width "[ ] "
	leftPad := strings.Repeat(" ", labelStart-cursorWidth)
	line := leftPad + f.cursorMark(i) + row.label + " " + valueChip(value)
	if row.unit != "" {
		line += " " + selflearnMutedStyle.Render(row.unit)
	}
	if row.footnote != nil {
		fn := row.footnote(row.intGetter(&f.snap))
		line += selflearnMutedStyle.Render("  ~  " + fn)
	}
	return line
}

// renderTextRow draws a free-text row ("Storage path  <value>") whose value is
// tail-anchored so the meaningful end of a long path stays visible; an empty,
// non-editing value shows the muted placeholder instead.
func (f *selfLearnForm) renderTextRow(i int, row configRow, width int) string {
	labelStart := contentCol(row.indent) + 4 // align with bool-row labels
	leftPad := strings.Repeat(" ", labelStart-cursorWidth)
	head := leftPad + f.cursorMark(i) + row.label + " "

	editing := f.editing && i == f.cursor
	value := row.strGetter(&f.snap)
	if editing {
		value = f.editingBuffer
	}

	avail := max(width-lipgloss.Width(head)-1, 8)
	if value == "" && !editing {
		return head + selflearnMutedStyle.Render(tailTruncate(row.placeholder, avail))
	}
	shown := tailTruncate(value, avail-1) // room for the edit caret
	if editing {
		shown += "_"
	}
	return head + selflearnValueStyle.Render(shown)
}

// tailTruncate keeps the last n columns of s, prefixing "…" when clipped, so a
// long path shows its most-specific tail rather than its common root.
func tailTruncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return "…" + string(r[len(r)-(n-1):])
}

// valueChip wraps a numeric value in chip-style brackets so it reads as
// an editable input: muted parens around an accent-bold value.
func valueChip(value string) string {
	return selflearnChipBracketStyle.Render("(") +
		selflearnValueStyle.Render(value) +
		selflearnChipBracketStyle.Render(")")
}

func (f *selfLearnForm) renderSaveRow(i int, validationErr error) string {
	style := selflearnSaveButtonStyle
	if validationErr != nil {
		style = selflearnSaveButtonDisabledStyle
	}
	btn := style.Render("Save")
	tail := selflearnMutedStyle.Render("  or " + keycap("esc") + selflearnMutedStyle.Render(" to discard"))
	return f.cursorPad(i, 1) + btn + tail
}

// ── Row kinds and layout ────────────────────────────────────────────────

// rowKind discriminates the rendered row types: bool toggle, int with a
// clamped range, the save action, two header levels, a blank spacer, and
// the advanced-action hint.
type rowKind int

const (
	rowBool rowKind = iota
	rowInt
	rowBoolInt // a checkbox with an inline editable number (Create + its cadence)
	rowText    // inline-edited free-text value (e.g. the memory storage path)
	rowEntry   // opens a panel-owned sub-view (e.g. the Steering Prompt editor)
	rowSave
	rowSubHeader // sub-section title (e.g. "Learn by")
	rowSpacer    // blank line
)

// configRow is one renderable row. Fields unused by the row's kind stay zero.
type configRow struct {
	kind       rowKind
	label      string
	toggle     func(*setting.SelfLearnSettings)
	boolGetter func(*setting.SelfLearnSettings) bool
	intGetter  func(*setting.SelfLearnSettings) int
	intSetter  func(*setting.SelfLearnSettings, int)
	intMin     int
	intMax     int
	unit       string           // for rowInt/rowBoolInt — muted suffix after the value (e.g. "iterations", "KB")
	intLead    string           // for rowBoolInt — muted word before the value chip (e.g. "every")
	footnote   func(int) string // for rowInt — optional muted inline footnote after the label
	note       string           // for rowBool — optional muted inline hint after the label
	indent     int
	// entryID identifies a rowEntry to its owning panel; desc/summary render
	// the muted description and the right-aligned value hint (e.g. "built-in").
	entryID string
	desc    string
	summary func(*setting.SelfLearnSettings) string
	// strGetter/strSetter back a rowText; placeholder shows when the value is
	// empty (e.g. "default (project store)").
	strGetter   func(*setting.SelfLearnSettings) string
	strSetter   func(*setting.SelfLearnSettings, string)
	placeholder string
}

// editable reports whether the row can hold the cursor. Derived from kind:
// the actionable kinds (toggle, edit-in-place, text, sub-view entry, save) are
// editable; section headers, sub-headers, and spacers are not.
func (r configRow) editable() bool {
	switch r.kind {
	case rowBool, rowInt, rowBoolInt, rowText, rowEntry, rowSave:
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

// Palette: teal (Focus) is the single accent, reserved for "focus / on" — the
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

	// Editable numeric value inside a "(…)" chip: plain bold text — the chip
	// brackets carry the "editable" affordance, no color needed.
	selflearnValueStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Text).Bold(true)

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

	// Faint wrapper for dimmed content (e.g. an unchecked action's cadence).
	selflearnFaintStyle = lipgloss.NewStyle().Faint(true)

	// Save button — a neutral filled pill; the teal cursor caret marks focus.
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

	// Chip-style brackets for editable values: "(10)" with dim parens.
	selflearnChipBracketStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim)

	// Right-aligned value hint on a sub-view entry row ("built-in" / "custom").
	selflearnEntrySummaryStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim)
)
