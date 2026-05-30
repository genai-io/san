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

// ConfigSelector is the /config Self-Learning settings panel. A
// multi-panel sidebar is planned (Provider, Permissions, Appearance, …);
// for now Self-Learning is the only entry, so the sidebar shows it
// pinned-and-selected. Future panels just add rows to the same scaffolding.
//
// The panel takes a working snapshot of selfLearn at Enter() time and
// mutates that snapshot as the user toggles / edits — disk is only touched
// on Save (s). Esc discards the snapshot. Inline validation (§3.1)
// disables Save and shows a red footer line while the snapshot is illegal.
type ConfigSelector struct {
	settings *setting.Settings

	active bool
	width  int
	height int

	// snap is the live editing buffer; saving merges it back to disk.
	snap setting.SelfLearnSettings
	// scope is which file Save targets (user-wide vs project-local).
	scope string // "user" | "project"

	cursor int // index into rows()

	// editing holds in-flight digits while the user types into an int field.
	editing       bool
	editingBuffer string
}

// ConfigSavedMsg is emitted on a successful Save so the app can show a
// transient confirmation.
type ConfigSavedMsg struct{ Scope string }

func NewConfigSelector(settings *setting.Settings) ConfigSelector {
	return ConfigSelector{settings: settings, scope: "user"}
}

// Enter activates the panel with a fresh snapshot of the current settings.
// Width / height are captured so Render can box the panel to the terminal.
func (c *ConfigSelector) Enter(width, height int) {
	c.width = width
	c.height = height
	c.active = true
	c.cursor = 0
	c.editing = false
	c.editingBuffer = ""
	if c.settings == nil {
		c.snap = setting.SelfLearnSettings{}
		return
	}
	if data := c.settings.Snapshot(); data != nil {
		c.snap = data.SelfLearn
	}
}

// IsActive implements the popup interface.
func (c *ConfigSelector) IsActive() bool { return c.active }

// HandleKeypress implements the popup interface.
func (c *ConfigSelector) HandleKeypress(msg tea.KeyMsg) tea.Cmd {
	if !c.active {
		return nil
	}
	if c.editing {
		return c.handleEditingKey(msg)
	}
	rows := c.rows()
	switch msg.String() {
	case "esc":
		c.active = false
		return nil
	case "up", "k":
		if c.cursor > 0 {
			c.cursor--
		}
	case "down", "j":
		if c.cursor+1 < len(rows) {
			c.cursor++
		}
	case "tab":
		// Toggle scope (user / project).
		if c.scope == "user" {
			c.scope = "project"
		} else {
			c.scope = "user"
		}
	case " ":
		// Space toggles bool rows.
		rows[c.cursor].toggle(&c.snap)
	case "enter":
		row := rows[c.cursor]
		switch row.kind {
		case rowBool:
			row.toggle(&c.snap)
		case rowInt:
			c.editing = true
			c.editingBuffer = strconv.Itoa(row.intGetter(&c.snap))
		case rowSave:
			if err := c.snap.Validate(); err != nil {
				return nil // surfaced as inline footer; ignore Enter
			}
			userLevel := c.scope == "user"
			if err := setting.UpdateSelfLearnAt(c.snap, userLevel); err != nil {
				return nil
			}
			scope := c.scope
			c.active = false
			return func() tea.Msg { return ConfigSavedMsg{Scope: scope} }
		}
	}
	return nil
}

// handleEditingKey applies digits / backspace / enter / esc while an int
// field has focus. Only digits 0-9 are accepted; the new value is clamped
// to the row's [min, max] range on confirm.
func (c *ConfigSelector) handleEditingKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		c.editing = false
		c.editingBuffer = ""
	case "enter":
		row := c.rows()[c.cursor]
		v, err := strconv.Atoi(c.editingBuffer)
		if err == nil {
			if v < row.intMin {
				v = row.intMin
			}
			if v > row.intMax {
				v = row.intMax
			}
			row.intSetter(&c.snap, v)
		}
		c.editing = false
		c.editingBuffer = ""
	case "backspace":
		if len(c.editingBuffer) > 0 {
			c.editingBuffer = c.editingBuffer[:len(c.editingBuffer)-1]
		}
	default:
		if len(msg.Runes) == 1 {
			r := msg.Runes[0]
			if r >= '0' && r <= '9' && len(c.editingBuffer) < 4 {
				c.editingBuffer += string(r)
			}
		}
	}
	return nil
}

// rowKind discriminates the editable field types: bool toggle, int with a
// clamped range, or the save action at the bottom.
type rowKind int

const (
	rowBool rowKind = iota
	rowInt
	rowSave
	rowHeader  // non-editable visual section title
	rowSpacer  // blank line
	rowAdvHint // ⚠ hint under allowUpdateUserCreated
)

// configRow is one renderable row in the panel. fields that are unused
// for the row's kind stay zero.
type configRow struct {
	kind       rowKind
	label      string
	tail       string                                              // right-aligned value text (computed at render)
	toggle     func(*setting.SelfLearnSettings)                    // for rowBool
	boolGetter func(*setting.SelfLearnSettings) bool               // for rowBool render
	intGetter  func(*setting.SelfLearnSettings) int                // for rowInt
	intSetter  func(*setting.SelfLearnSettings, int)               // for rowInt
	intMin     int                                                 // for rowInt
	intMax     int                                                 // for rowInt
	editable   bool                                                // whether ↑↓ stops here
	indent     int                                                 // visual indent level
}

// rows materializes the panel layout: section headers + every editable
// field. Computed fresh per call so the same function drives both the
// keypress handler (for navigation bounds) and Render.
func (c *ConfigSelector) rows() []configRow {
	return []configRow{
		{kind: rowHeader, label: "Memory"},
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
			intGetter: func(s *setting.SelfLearnSettings) int { return defaultIfZero(s.Memory.MaxKB, setting.SelfLearnDefaultMemoryKB) },
			intSetter: func(s *setting.SelfLearnSettings, v int) { s.Memory.MaxKB = v },
			intMin:    1,
			intMax:    setting.SelfLearnMaxMemoryKB,
		},
		{kind: rowSpacer},
		{kind: rowHeader, label: "Skills"},
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
		{kind: rowHeader, label: "  Allowed actions (agent-created scope)", indent: 1},
		{
			kind:       rowBool,
			label:      "Create new skills",
			indent:     2,
			editable:   true,
			boolGetter: func(s *setting.SelfLearnSettings) bool { return s.Skills.AllowCreateOr() },
			toggle:     toggleAllowCreate,
		},
		{
			kind:       rowBool,
			label:      "Update existing skills",
			indent:     2,
			editable:   true,
			boolGetter: func(s *setting.SelfLearnSettings) bool { return s.Skills.AllowUpdateOr() },
			toggle:     toggleAllowUpdate,
		},
		{
			kind:       rowBool,
			label:      "Delete obsolete skills",
			indent:     2,
			editable:   true,
			boolGetter: func(s *setting.SelfLearnSettings) bool { return s.Skills.AllowDeleteOr() },
			toggle:     toggleAllowDelete,
		},
		{kind: rowSpacer},
		{kind: rowHeader, label: "  Advanced", indent: 1},
		{
			kind:       rowBool,
			label:      "Update user-authored skills",
			indent:     2,
			editable:   true,
			boolGetter: func(s *setting.SelfLearnSettings) bool { return s.Skills.AllowUpdateUserCreated },
			toggle:     func(s *setting.SelfLearnSettings) { s.Skills.AllowUpdateUserCreated = !s.Skills.AllowUpdateUserCreated },
		},
		{kind: rowAdvHint, label: "⚠ rewrites your authored skill files"},
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

// toggleAllowCreate / toggleAllowUpdate / toggleAllowDelete flip the *bool
// fields, going from "nil (default true)" through "explicit false" back to
// "explicit true". This three-state cycle is what lets the user move
// between the four §5.5 legal combinations.
func toggleAllowCreate(s *setting.SelfLearnSettings) {
	v := s.Skills.AllowCreateOr()
	s.Skills.AllowCreate = boolPtr(!v)
}

func toggleAllowUpdate(s *setting.SelfLearnSettings) {
	v := s.Skills.AllowUpdateOr()
	s.Skills.AllowUpdate = boolPtr(!v)
}

func toggleAllowDelete(s *setting.SelfLearnSettings) {
	v := s.Skills.AllowDeleteOr()
	s.Skills.AllowDelete = boolPtr(!v)
}

func boolPtr(b bool) *bool { return &b }

// ── Rendering ────────────────────────────────────────────────────────────

var (
	configBorderStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.RoundedBorder()).
				BorderForeground(kit.CurrentTheme.Muted).
				Padding(0, 2)
	configHeaderStyle = lipgloss.NewStyle().
				Foreground(kit.CurrentTheme.Accent).
				Bold(true)
	configCursorStyle = lipgloss.NewStyle().
				Foreground(kit.CurrentTheme.Accent).
				Bold(true)
	configMutedStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	configErrorStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Error)
	configHintStyle  = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Warning).Italic(true)
	configOKStyle    = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Success)
)

// Render implements the popup interface.
func (c *ConfigSelector) Render() string {
	rows := c.rows()
	// Cache validation once — Render and the Save row both consult it; we
	// don't want it to drift between branches at terminal redraw rate.
	validationErr := c.snap.Validate()

	var b strings.Builder
	title := configHeaderStyle.Render(fmt.Sprintf("Self-Learning ▸ scope: %s (Tab to toggle)", c.scope))
	b.WriteString(title)
	b.WriteString("\n\n")

	for i, row := range rows {
		switch row.kind {
		case rowHeader:
			b.WriteString(configHeaderStyle.Render(prefix(row.indent) + row.label))
		case rowSpacer:
			b.WriteString(" ")
		case rowAdvHint:
			b.WriteString(configHintStyle.Render(prefix(row.indent) + "    " + row.label))
		case rowSave:
			cursor := " "
			if i == c.cursor {
				cursor = configCursorStyle.Render("▸")
			}
			label := "[ Save ]"
			if validationErr != nil {
				label = configMutedStyle.Render(label)
			} else {
				label = configOKStyle.Render(label)
			}
			b.WriteString(cursor + " " + label)
		case rowBool:
			b.WriteString(c.renderBoolRow(i, row))
		case rowInt:
			b.WriteString(c.renderIntRow(i, row))
		}
		b.WriteString("\n")
	}

	if validationErr != nil {
		b.WriteString("\n")
		b.WriteString(configErrorStyle.Render("⚠ " + validationErr.Error()))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(configMutedStyle.Render(
		"↑↓ navigate · space toggle · enter edit/save · tab scope · esc cancel"))

	return configBorderStyle.Render(b.String())
}

func (c *ConfigSelector) renderBoolRow(i int, row configRow) string {
	cursor := " "
	if i == c.cursor {
		cursor = configCursorStyle.Render("▸")
	}
	mark := "[ ]"
	if row.boolGetter(&c.snap) {
		mark = "[✓]"
	}
	label := prefix(row.indent) + row.label
	return cursor + " " + mark + " " + label
}

func (c *ConfigSelector) renderIntRow(i int, row configRow) string {
	cursor := " "
	if i == c.cursor {
		cursor = configCursorStyle.Render("▸")
	}
	value := strconv.Itoa(row.intGetter(&c.snap))
	if c.editing && i == c.cursor {
		value = c.editingBuffer + "_"
	}
	label := prefix(row.indent) + row.label
	tail := ""
	switch row.label {
	case "Max size (KB)":
		// Show the ≈ word / 中文字 equivalence next to the value.
		v := row.intGetter(&c.snap)
		tail = configMutedStyle.Render(fmt.Sprintf(
			"   ≈ %d EN words / %d 中文字 (UTF-8)", v*180, v*340))
	}
	return fmt.Sprintf("%s %s  ⟨ %s ⟩%s", cursor, label, value, tail)
}

func prefix(indent int) string { return strings.Repeat("  ", indent) }
