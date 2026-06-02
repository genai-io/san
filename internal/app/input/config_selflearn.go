// /config Self-Learning panel: edits setting.SelfLearnSettings against a
// working snapshot, validates the §3.1 invariants inline, and writes to
// either the user-level or project-level settings file on Save.
package input

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/genai-io/gen-code/internal/app/kit"
	"github.com/genai-io/gen-code/internal/setting"
)

type selfLearnPanel struct {
	settings *setting.Settings

	// snap is the working buffer; Save merges it back to disk.
	snap setting.SelfLearnSettings
	// baseline captures snap as it was at Enter() time so the panel can
	// flag unsaved edits in the top-right corner.
	baseline setting.SelfLearnSettings
	scope    string // "user" | "project"

	cursor        int
	editing       bool
	editingBuffer string
}

func newSelfLearnPanel(settings *setting.Settings) *selfLearnPanel {
	return &selfLearnPanel{settings: settings, scope: "user"}
}

func (p *selfLearnPanel) Title() string { return "self-learning" }

func (p *selfLearnPanel) Enter() {
	p.editing = false
	p.editingBuffer = ""
	if p.settings == nil {
		p.snap = setting.SelfLearnSettings{}
	} else if data := p.settings.Snapshot(); data != nil {
		p.snap = data.SelfLearn
	}
	p.baseline = p.snap
	p.cursor = firstEditableRow(p.rows())
}

// dirty reports whether the working snapshot diverges from the disk
// baseline — drives the "● unsaved" indicator.
func (p *selfLearnPanel) dirty() bool { return p.snap != p.baseline }

func (p *selfLearnPanel) HandleKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	if p.editing {
		return p.handleEditingKey(msg), false
	}
	rows := p.rows()
	if p.cursor >= len(rows) {
		p.cursor = firstEditableRow(rows)
	}
	switch msg.String() {
	case "up", "k":
		p.cursor = prevEditableRow(rows, p.cursor)
	case "down", "j":
		p.cursor = nextEditableRow(rows, p.cursor)
	case "tab":
		if p.scope == "user" {
			p.scope = "project"
		} else {
			p.scope = "user"
		}
	case " ":
		if r := rows[p.cursor]; r.kind == rowBool && r.toggle != nil {
			r.toggle(&p.snap)
		}
	case "enter":
		row := rows[p.cursor]
		switch row.kind {
		case rowBool:
			if row.toggle != nil {
				row.toggle(&p.snap)
			}
		case rowInt:
			p.editing = true
			p.editingBuffer = strconv.Itoa(row.intGetter(&p.snap))
		case rowSave:
			if err := p.snap.Validate(); err != nil {
				return nil, false
			}
			userLevel := p.scope == "user"
			if err := setting.UpdateSelfLearnAt(p.snap, userLevel); err != nil {
				return nil, false
			}
			scope := p.scope
			return func() tea.Msg { return ConfigSavedMsg{Scope: scope} }, true
		}
	}
	return nil, false
}

func (p *selfLearnPanel) handleEditingKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		p.editing = false
		p.editingBuffer = ""
	case "enter":
		row := p.rows()[p.cursor]
		if v, err := strconv.Atoi(p.editingBuffer); err == nil {
			if v < row.intMin {
				v = row.intMin
			}
			if v > row.intMax {
				v = row.intMax
			}
			row.intSetter(&p.snap, v)
		}
		p.editing = false
		p.editingBuffer = ""
	case "backspace":
		if n := len(p.editingBuffer); n > 0 {
			p.editingBuffer = p.editingBuffer[:n-1]
		}
	default:
		if len(msg.Runes) == 1 {
			r := msg.Runes[0]
			if r >= '0' && r <= '9' && len(p.editingBuffer) < 4 {
				p.editingBuffer += string(r)
			}
		}
	}
	return nil
}

// HintLine renders the bottom hint as monospace keycaps.
func (p *selfLearnPanel) HintLine() string {
	return keycap("↑↓") + " navigate  " +
		keycap("space") + " toggle  " +
		keycap("enter") + " edit/save  " +
		keycap("tab") + " scope"
}

// Render draws the panel body.
func (p *selfLearnPanel) Render(width int) string {
	rows := p.rows()
	validationErr := p.snap.Validate()

	var b strings.Builder
	if dirty := p.renderUnsaved(width); dirty != "" {
		b.WriteString(dirty)
		b.WriteString("\n\n")
	}
	b.WriteString(p.renderScopeControl())
	b.WriteString("\n\n")

	rail := ""             // current section's styled rail prefix
	sectionEnabled := true // whether the current section is "on"
	for i, row := range rows {
		switch row.kind {
		case rowSectionHeader:
			b.WriteString(p.renderSectionHeader(row, width))
			rail = p.railFor(row)
			sectionEnabled = row.enabledFn == nil || row.enabledFn(&p.snap)
		case rowSubHeader:
			indentPad := strings.Repeat(" ", contentCol(row.indent)-1)
			line := indentPad + selflearnSubHeaderStyle.Render(row.label)
			b.WriteString(rail + p.maybeDim(sectionEnabled, line))
		case rowSpacer:
			// Skip the rail on trailing spacers so the bar visibly closes
			// at the last content row of the section.
			if !p.isTrailingSpacer(rows, i) {
				b.WriteString(rail)
			}
		case rowSave:
			b.WriteString(p.renderSaveRow(i, validationErr))
		case rowBool:
			line := p.renderBoolRow(i, row, width)
			b.WriteString(p.withRail(rail, p.maybeDim(sectionEnabled, line)))
		case rowInt:
			line := p.renderIntRow(i, row, width)
			b.WriteString(p.withRail(rail, p.maybeDim(sectionEnabled, line)))
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

// maybeDim wraps the rendered row in Faint when the parent section is
// off, so disabled-section children stop reading as "active".
func (p *selfLearnPanel) maybeDim(enabled bool, line string) string {
	if enabled {
		return line
	}
	return selflearnFaintStyle.Render(line)
}

// isTrailingSpacer reports whether row i is a spacer with no further
// content rows in the current section.
func (p *selfLearnPanel) isTrailingSpacer(rows []configRow, i int) bool {
	for j := i + 1; j < len(rows); j++ {
		switch rows[j].kind {
		case rowSpacer:
			continue
		case rowSectionHeader, rowSave:
			return true
		default:
			return false
		}
	}
	return true
}

// renderUnsaved is the right-aligned "● unsaved" tag; returns "" when clean.
func (p *selfLearnPanel) renderUnsaved(width int) string {
	if !p.dirty() {
		return ""
	}
	tag := selflearnUnsavedDotStyle.Render("●") + " " +
		selflearnUnsavedTextStyle.Render("unsaved")
	pad := max(width-9, 0) // "● unsaved" = 9 visible cells
	return strings.Repeat(" ", pad) + tag
}

// renderScopeControl is a two-segment selector with the active segment
// in accent style.
func (p *selfLearnPanel) renderScopeControl() string {
	seg := func(name string) string {
		if p.scope == name {
			return selflearnScopeActiveStyle.Render(name)
		}
		return selflearnScopeIdleStyle.Render(name)
	}
	sep := selflearnMutedStyle.Render("  ·  ")
	return selflearnMutedStyle.Render("scope  ") + seg("user") + sep + seg("project")
}

// renderSectionHeader prints "MEMORY ─────…" — label in caps, then a
// hairline rule filling the rest of the row.
func (p *selfLearnPanel) renderSectionHeader(row configRow, width int) string {
	label := strings.ToUpper(row.label)
	ruleLen := max(width-len(label)-1, 1)
	return selflearnSectionStyle.Render(label) + " " +
		selflearnRuleStyle.Render(strings.Repeat("─", ruleLen))
}

// railFor returns the left-edge rail for a section's content. The shape
// AND color carry the on/off signal so the two states pop visually:
//   - on:  "┃" — heavy vertical bar in accent green
//   - off: "╎" — dashed light bar in muted text-dim
//
// This way the active section's rail clearly outweighs the section-
// divider hairlines ("MEMORY ────"), making "which one is running" the
// loudest signal on the panel.
func (p *selfLearnPanel) railFor(row configRow) string {
	if row.enabledFn != nil && row.enabledFn(&p.snap) {
		return selflearnRailOnStyle.Render("┃")
	}
	return selflearnRailOffStyle.Render("╎")
}

// withRail prepends the section rail to a row, consuming its first
// blank column so the rest of the column math stays consistent.
func (p *selfLearnPanel) withRail(rail, row string) string {
	if rail == "" {
		return row
	}
	if len(row) > 0 && row[0] == ' ' {
		return rail + row[1:]
	}
	return rail + row
}

// keycap renders a key label as a bg-filled pill so it doesn't read as
// a checkbox. The fill is the kit's neutral search-input gray so the
// keycap feels like a physical key cap, not a [ ] toggle.
func keycap(s string) string {
	return selflearnKeycapStyle.Render(" " + s + " ")
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

func (p *selfLearnPanel) cursorMark(i int) string {
	if i == p.cursor {
		return selflearnCursorStyle.Render("▸ ")
	}
	return strings.Repeat(" ", cursorWidth)
}

// cursorPad returns the leading whitespace + cursor caret so the next
// glyph lands exactly at contentCol(indent).
func (p *selfLearnPanel) cursorPad(i, indent int) string {
	at := max(contentCol(indent)-cursorWidth, 0)
	return strings.Repeat(" ", at) + p.cursorMark(i)
}

func (p *selfLearnPanel) renderBoolRow(i int, row configRow, _ int) string {
	mark := "[ ]"
	if row.boolGetter(&p.snap) {
		mark = selflearnCheckStyle.Render("[✓]")
	}
	line := p.cursorPad(i, row.indent) + mark + " " + row.label
	if row.advHint != "" {
		line += "  " + selflearnHintStyle.Render(row.advHint)
	}
	return line
}

func (p *selfLearnPanel) renderIntRow(i int, row configRow, _ int) string {
	value := strconv.Itoa(row.intGetter(&p.snap))
	if p.editing && i == p.cursor {
		value = p.editingBuffer + "_"
	}
	// Label-first phrase: "Run every (10) user turns" reads as a sentence.
	// The label sits one bracket-width past where a bool row's "[" goes,
	// so labels align with bool-row labels at the same indent.
	labelStart := contentCol(row.indent) + 4 // 4 = bracket width "[ ] "
	leftPad := strings.Repeat(" ", labelStart-cursorWidth)
	line := leftPad + p.cursorMark(i) + row.label + " " + valueChip(value)
	if row.unit != "" {
		line += " " + selflearnMutedStyle.Render(row.unit)
	}
	if row.footnote != nil {
		fn := row.footnote(row.intGetter(&p.snap))
		line += selflearnMutedStyle.Render("  ~  " + fn)
	}
	return line
}

// valueChip wraps a numeric value in chip-style brackets so it reads as
// an editable input: muted parens around an accent-bold value.
func valueChip(value string) string {
	return selflearnChipBracketStyle.Render("(") +
		selflearnValueStyle.Render(value) +
		selflearnChipBracketStyle.Render(")")
}

func (p *selfLearnPanel) renderSaveRow(i int, validationErr error) string {
	style := selflearnSaveButtonStyle
	if validationErr != nil {
		style = selflearnSaveButtonDisabledStyle
	}
	btn := style.Render("Save")
	tail := selflearnMutedStyle.Render("  or " + keycap("esc") + selflearnMutedStyle.Render(" to discard"))
	return p.cursorPad(i, 1) + btn + tail
}

// ── Row kinds and layout ────────────────────────────────────────────────

// rowKind discriminates the rendered row types: bool toggle, int with a
// clamped range, the save action, two header levels, a blank spacer, and
// the advanced-action hint.
type rowKind int

const (
	rowBool rowKind = iota
	rowInt
	rowSave
	rowSectionHeader // big section title (Memory / Skills)
	rowSubHeader     // sub-section title (Allowed actions / Advanced)
	rowSpacer        // blank line
)

// configRow is one renderable row. Fields unused by the row's kind stay zero.
type configRow struct {
	kind       rowKind
	label      string
	toggle     func(*setting.SelfLearnSettings)
	boolGetter func(*setting.SelfLearnSettings) bool
	intGetter  func(*setting.SelfLearnSettings) int
	intSetter  func(*setting.SelfLearnSettings, int)
	// enabledFn is set on rowSectionHeader to tell the renderer whether
	// the section is "on" (drives the vertical rail color).
	enabledFn func(*setting.SelfLearnSettings) bool
	intMin    int
	intMax    int
	unit      string           // for rowInt — muted suffix after the value (e.g. "user turns", "KB")
	footnote  func(int) string // for rowInt — optional muted inline footnote after the label
	advHint   string           // for rowBool — optional inline "⚠ …" hint after the label
	editable  bool
	indent    int
}

func (p *selfLearnPanel) rows() []configRow {
	return []configRow{
		{kind: rowSectionHeader, label: "Memory", enabledFn: func(s *setting.SelfLearnSettings) bool { return s.Memory.Enabled }},
		{
			kind:       rowBool,
			label:      "Enable memory-evolving",
			indent:     1,
			editable:   true,
			boolGetter: func(s *setting.SelfLearnSettings) bool { return s.Memory.Enabled },
			toggle:     func(s *setting.SelfLearnSettings) { s.Memory.Enabled = !s.Memory.Enabled },
		},
		{
			kind:      rowInt,
			label:     "Run every",
			unit:      "user turns",
			indent:    1,
			editable:  true,
			intGetter: func(s *setting.SelfLearnSettings) int { return defaultIfZero(s.Memory.EveryTurns, 10) },
			intSetter: func(s *setting.SelfLearnSettings, v int) { s.Memory.EveryTurns = v },
			intMin:    1,
			intMax:    100,
		},
		{
			kind:      rowInt,
			label:     "Max size",
			unit:      "KB",
			indent:    1,
			editable:  true,
			intGetter: func(s *setting.SelfLearnSettings) int { return s.Memory.MaxKBOr() },
			intSetter: func(s *setting.SelfLearnSettings, v int) { s.Memory.MaxKB = v },
			intMin:    1,
			intMax:    setting.SelfLearnMaxMemoryKB,
			footnote: func(v int) string {
				return fmt.Sprintf("%d EN words / %d 中文字 (UTF-8)", v*180, v*340)
			},
		},
		{kind: rowSpacer},
		{kind: rowSectionHeader, label: "Skills", enabledFn: func(s *setting.SelfLearnSettings) bool { return s.Skills.Enabled }},
		{
			kind:       rowBool,
			label:      "Enable skill-evolving",
			indent:     1,
			editable:   true,
			boolGetter: func(s *setting.SelfLearnSettings) bool { return s.Skills.Enabled },
			toggle:     func(s *setting.SelfLearnSettings) { s.Skills.Enabled = !s.Skills.Enabled },
		},
		{
			kind:      rowInt,
			label:     "Run every",
			unit:      "tool iterations",
			indent:    1,
			editable:  true,
			intGetter: func(s *setting.SelfLearnSettings) int { return defaultIfZero(s.Skills.EveryToolIters, 10) },
			intSetter: func(s *setting.SelfLearnSettings, v int) { s.Skills.EveryToolIters = v },
			intMin:    1,
			intMax:    100,
		},
		{kind: rowSpacer},
		{kind: rowSubHeader, label: "Allowed actions (agent-created scope)", indent: 1},
		{
			kind:       rowBool,
			label:      "Create new skills",
			indent:     2,
			editable:   true,
			boolGetter: func(s *setting.SelfLearnSettings) bool { return s.Skills.AllowCreate() },
			toggle:     func(s *setting.SelfLearnSettings) { s.Skills.DenyCreate = !s.Skills.DenyCreate },
		},
		{
			kind:       rowBool,
			label:      "Update existing skills",
			indent:     2,
			editable:   true,
			boolGetter: func(s *setting.SelfLearnSettings) bool { return s.Skills.AllowUpdate() },
			toggle:     func(s *setting.SelfLearnSettings) { s.Skills.DenyUpdate = !s.Skills.DenyUpdate },
		},
		{
			kind:       rowBool,
			label:      "Delete obsolete skills",
			indent:     2,
			editable:   true,
			boolGetter: func(s *setting.SelfLearnSettings) bool { return s.Skills.AllowDelete() },
			toggle:     func(s *setting.SelfLearnSettings) { s.Skills.DenyDelete = !s.Skills.DenyDelete },
		},
		{kind: rowSpacer},
		{kind: rowSubHeader, label: "Advanced", indent: 1},
		{
			kind:       rowBool,
			label:      "Update user-authored skills",
			indent:     2,
			editable:   true,
			boolGetter: func(s *setting.SelfLearnSettings) bool { return s.Skills.AllowUpdateUserCreated },
			toggle:     func(s *setting.SelfLearnSettings) { s.Skills.AllowUpdateUserCreated = !s.Skills.AllowUpdateUserCreated },
			advHint:    "⚠ rewrites your authored skill files",
		},
		{kind: rowSpacer},
		{kind: rowSave, label: "Save", editable: true},
	}
}

func defaultIfZero(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}

func firstEditableRow(rows []configRow) int {
	for i, r := range rows {
		if r.editable {
			return i
		}
	}
	return 0
}

func nextEditableRow(rows []configRow, cur int) int {
	for i := cur + 1; i < len(rows); i++ {
		if rows[i].editable {
			return i
		}
	}
	return cur
}

func prevEditableRow(rows []configRow, cur int) int {
	for i := cur - 1; i >= 0; i-- {
		if rows[i].editable {
			return i
		}
	}
	return cur
}

// ── Styles ──────────────────────────────────────────────────────────────

var (
	selflearnSectionStyle   = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Accent).Bold(true)
	selflearnSubHeaderStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Text).Bold(true)
	selflearnMutedStyle     = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	selflearnHintStyle      = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Warning).Italic(true)
	selflearnErrorStyle     = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Error)
	selflearnCursorStyle    = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Accent).Bold(true)
	selflearnCheckStyle     = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Success)
	selflearnValueStyle     = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Accent).Underline(true)
	selflearnRuleStyle      = lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim).Faint(true)

	// Vertical rail along the left of a section's content. Color reflects
	// the section's enabled state — green when "on", muted when "off".
	selflearnRailOnStyle  = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Success).Bold(true)
	selflearnRailOffStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim)

	// Two-segment scope control: active is a filled pill (accent bg +
	// background fg), inactive is a flat padded label so the two segments
	// read as a real segmented control.
	selflearnScopeActiveStyle = lipgloss.NewStyle().
					Background(kit.CurrentTheme.Accent).
					Foreground(kit.CurrentTheme.Background).
					Bold(true).
					Padding(0, 1)
	selflearnScopeIdleStyle = lipgloss.NewStyle().
				Foreground(kit.CurrentTheme.TextDim).
				Padding(0, 1)

	// "● unsaved" tag for the top-right corner of the panel body.
	selflearnUnsavedDotStyle  = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Warning).Bold(true)
	selflearnUnsavedTextStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Warning)

	// Keycap pill — neutral gray bg + bold text so it visibly diverges
	// from "[ ]" checkboxes and reads as a physical key.
	selflearnKeycapStyle = lipgloss.NewStyle().
				Background(kit.SearchBg).
				Foreground(kit.CurrentTheme.Text).
				Bold(true)

	// Faint wrapper for disabled-section children.
	selflearnFaintStyle = lipgloss.NewStyle().Faint(true)

	// Save button — filled accent pill when ready, muted pill when the
	// snapshot fails validation.
	selflearnSaveButtonStyle = lipgloss.NewStyle().
					Background(kit.CurrentTheme.Success).
					Foreground(kit.CurrentTheme.Background).
					Bold(true).
					Padding(0, 2)
	selflearnSaveButtonDisabledStyle = lipgloss.NewStyle().
						Background(kit.CurrentTheme.TextDim).
						Foreground(kit.CurrentTheme.Background).
						Padding(0, 2)

	// Chip-style brackets for editable values: "(10)" with muted parens.
	selflearnChipBracketStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim)
)
