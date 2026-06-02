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
	snap  setting.SelfLearnSettings
	scope string // "user" | "project"

	cursor        int
	editing       bool
	editingBuffer string
}

func newSelfLearnPanel(settings *setting.Settings) *selfLearnPanel {
	return &selfLearnPanel{settings: settings, scope: "user"}
}

func (p *selfLearnPanel) Title() string { return "Self-Learning" }

func (p *selfLearnPanel) Enter() {
	p.editing = false
	p.editingBuffer = ""
	if p.settings == nil {
		p.snap = setting.SelfLearnSettings{}
	} else if data := p.settings.Snapshot(); data != nil {
		p.snap = data.SelfLearn
	}
	p.cursor = firstEditableRow(p.rows())
}

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

func (p *selfLearnPanel) HintLine() string {
	return "↑↓ navigate · space toggle · enter edit/save · tab scope"
}

// Render draws the panel body: scope chip, sections, rows, validation
// error. width is the inner width allocated by the shell.
func (p *selfLearnPanel) Render(width int) string {
	rows := p.rows()
	validationErr := p.snap.Validate()

	var b strings.Builder
	b.WriteString(p.renderScopeBar())
	b.WriteString("\n\n")

	for i, row := range rows {
		switch row.kind {
		case rowSectionHeader:
			b.WriteString(selflearnSectionStyle.Render(prefix(row.indent) + row.label))
		case rowSubHeader:
			b.WriteString(selflearnSubHeaderStyle.Render(prefix(row.indent) + row.label))
		case rowSpacer:
			b.WriteString(" ")
		case rowAdvHint:
			b.WriteString(selflearnHintStyle.Render(prefix(row.indent) + row.label))
		case rowSave:
			b.WriteString(p.renderSaveRow(i, validationErr))
		case rowBool:
			b.WriteString(p.renderBoolRow(i, row, width))
		case rowInt:
			b.WriteString(p.renderIntRow(i, row, width))
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

func (p *selfLearnPanel) renderScopeBar() string {
	other := "project"
	if p.scope == "project" {
		other = "user"
	}
	chip := selflearnScopeChipStyle.Render(" " + p.scope + " ")
	hint := selflearnMutedStyle.Render(" · tab to switch to " + other)
	return selflearnMutedStyle.Render("scope  ") + chip + hint
}

// ── Row rendering ───────────────────────────────────────────────────────

const (
	// cursorCol holds the cursor caret; the rest of the body is right-padded
	// from there so every row's content lines up no matter the cursor state.
	cursorCol = "  " // 2 columns (caret + space)
	checkCol  = "[ ]"
)

func (p *selfLearnPanel) cursorMark(i int) string {
	if i == p.cursor {
		return selflearnCursorStyle.Render("▸ ")
	}
	return cursorCol
}

func (p *selfLearnPanel) renderBoolRow(i int, row configRow, _ int) string {
	mark := checkCol
	if row.boolGetter(&p.snap) {
		mark = selflearnCheckStyle.Render("[✓]")
	}
	return p.cursorMark(i) + prefix(row.indent) + mark + " " + row.label
}

func (p *selfLearnPanel) renderIntRow(i int, row configRow, width int) string {
	value := strconv.Itoa(row.intGetter(&p.snap))
	if p.editing && i == p.cursor {
		value = p.editingBuffer + "_"
	}
	label := prefix(row.indent) + row.label
	valueCell := selflearnValueStyle.Render(value)

	// Right-align the value cell. Compute the gap between label and value.
	// width = inner panel width; cursor (2) + label + gap + value rendered.
	leftLen := len(cursorCol) + visibleLen(label) + 1 // +1 trailing space before value
	valueLen := visibleLen(value)
	pad := max(width-leftLen-valueLen, 1)
	line := p.cursorMark(i) + label + strings.Repeat(" ", pad) + valueCell

	// Tail equivalence for memory.maxKB — placed under the value, indented
	// past the label column so it reads as a footnote.
	if row.label == "Max size (KB)" {
		v := row.intGetter(&p.snap)
		eq := selflearnMutedStyle.Render(fmt.Sprintf("≈ %d EN words / %d 中文字 (UTF-8)", v*180, v*340))
		line += "\n" + cursorCol + prefix(row.indent+2) + eq
	}
	return line
}

func (p *selfLearnPanel) renderSaveRow(i int, validationErr error) string {
	label := "Save"
	style := selflearnSaveReadyStyle
	if validationErr != nil {
		style = selflearnSaveDisabledStyle
	}
	return p.cursorMark(i) + style.Render("[ "+label+" ]")
}

// visibleLen approximates the column width of s in a monospace cell. It
// treats every rune as one column; full-width CJK runes (rare in this
// panel — only the validation error path) overflow by a column or two,
// which we accept rather than pulling in a runewidth dep.
func visibleLen(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
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
	rowAdvHint       // ⚠ hint under allowUpdateUserCreated
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
	editable   bool
	indent     int
}

func (p *selfLearnPanel) rows() []configRow {
	return []configRow{
		{kind: rowSectionHeader, label: "Memory"},
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
			label:     "Review cadence (user turns)",
			indent:    2,
			editable:  true,
			intGetter: func(s *setting.SelfLearnSettings) int { return defaultIfZero(s.Memory.EveryTurns, 10) },
			intSetter: func(s *setting.SelfLearnSettings, v int) { s.Memory.EveryTurns = v },
			intMin:    1,
			intMax:    100,
		},
		{
			kind:      rowInt,
			label:     "Max size (KB)",
			indent:    2,
			editable:  true,
			intGetter: func(s *setting.SelfLearnSettings) int { return s.Memory.MaxKBOr() },
			intSetter: func(s *setting.SelfLearnSettings, v int) { s.Memory.MaxKB = v },
			intMin:    1,
			intMax:    setting.SelfLearnMaxMemoryKB,
		},
		{kind: rowSpacer},
		{kind: rowSectionHeader, label: "Skills"},
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
			label:     "Review cadence (tool iterations)",
			indent:    2,
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
		},
		{kind: rowAdvHint, label: "⚠ rewrites your authored skill files", indent: 3},
		{kind: rowSpacer},
		{kind: rowSave, label: "Save", editable: true, indent: 1},
	}
}

func defaultIfZero(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}

func prefix(indent int) string { return strings.Repeat("  ", indent) }

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
	selflearnValueStyle     = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Accent)

	selflearnScopeChipStyle = lipgloss.NewStyle().
				Foreground(kit.CurrentTheme.Background).
				Background(kit.CurrentTheme.Accent).
				Bold(true)

	selflearnSaveReadyStyle    = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Success).Bold(true)
	selflearnSaveDisabledStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
)
