// /config Appearance panel: a radio list that switches the color theme
// (light / dark / auto). Selecting a row applies the theme live to the
// running TUI and persists it to the user-level settings file. Theme is a
// personal preference, so — unlike Self-Learning — it has no project scope.
package selector

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/setting"
)

// ThemeSavedMsg is emitted after the appearance panel persists a theme so
// the app can refresh its settings handle and show a confirmation. The
// theme is already applied (kit.InitTheme) and written to disk by the time
// this fires.
type ThemeSavedMsg struct {
	Theme string
}

type themeChoice struct {
	label, value, desc string
}

// appearanceThemeChoices mirrors the first-run selector (kit.RunThemeSelector)
// so the in-app switcher and the first-run prompt offer the same options.
var appearanceThemeChoices = []themeChoice{
	{"Dark", "dark", "Dark background terminal"},
	{"Light", "light", "Light background terminal"},
	{"Auto", "auto", "Match terminal appearance automatically"},
}

type appearancePanel struct {
	settings *setting.Settings

	// baseline is the theme persisted on disk, marked "● current" in the
	// list; cursor is the row the user is hovering. Dirty when they differ.
	baseline string
	cursor   int
	// saveErr holds the last failed persist so Render can surface it inline
	// instead of silently swallowing it. Cleared on navigation / re-entry.
	saveErr error
}

func newAppearancePanel(settings *setting.Settings) *appearancePanel {
	return &appearancePanel{settings: settings}
}

func (p *appearancePanel) Title() string { return "appearance" }

func (p *appearancePanel) Enter() {
	p.baseline = "auto"
	if p.settings != nil {
		if data := p.settings.Snapshot(); data != nil && data.Theme != "" {
			p.baseline = data.Theme
		}
	}
	p.cursor = indexOfTheme(p.baseline)
	p.saveErr = nil
}

// Dirty reports whether the hovered row diverges from the saved theme, so
// the shell pins the "● unsaved" indicator.
func (p *appearancePanel) Dirty() bool {
	return appearanceThemeChoices[p.cursor].value != p.baseline
}

func (p *appearancePanel) HandleKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
		}
		p.saveErr = nil
	case "down", "j":
		if p.cursor < len(appearanceThemeChoices)-1 {
			p.cursor++
		}
		p.saveErr = nil
	case "enter", " ":
		value := appearanceThemeChoices[p.cursor].value
		// Apply live so the switch is visible immediately. InitTheme only
		// flips lipgloss's cached background flag (no terminal I/O), so it is
		// safe to call mid-program.
		kit.InitTheme(value)
		if err := setting.SaveTheme(value); err != nil {
			// Keep the on-screen theme in sync with what's actually persisted:
			// revert the live apply and surface the error instead of leaving
			// the UI showing a theme that won't survive a restart.
			kit.InitTheme(p.baseline)
			p.saveErr = err
			return nil, false
		}
		p.baseline = value
		return func() tea.Msg { return ThemeSavedMsg{Theme: value} }, true
	}
	return nil, false
}

func (p *appearancePanel) HintLine() string {
	return keycap("↑↓") + " navigate  " + keycap("enter") + " apply"
}

func (p *appearancePanel) Render(width int) string {
	var b strings.Builder
	b.WriteString(appearanceSectionStyle.Render("COLOR THEME"))
	b.WriteString(" ")
	ruleLen := max(width-len("COLOR THEME")-1, 1)
	b.WriteString(appearanceRuleStyle.Render(strings.Repeat("─", ruleLen)))
	b.WriteString("\n\n")

	for i, opt := range appearanceThemeChoices {
		caret := "  "
		label := appearanceLabelStyle.Render(opt.label)
		if i == p.cursor {
			caret = appearanceCursorStyle.Render("▸ ")
			label = appearanceCursorStyle.Render(opt.label)
		}

		radio := appearanceRadioOffStyle.Render("○")
		current := ""
		if opt.value == p.baseline {
			radio = appearanceRadioOnStyle.Render("●")
			current = "  " + appearanceCurrentStyle.Render("current")
		}

		// Pad labels to a common column so the descriptions line up.
		labelCell := label + strings.Repeat(" ", max(8-len(opt.label), 1))
		b.WriteString(caret + radio + " " + labelCell + appearanceDescStyle.Render(opt.desc) + current)
		b.WriteString("\n")
	}

	if p.saveErr != nil {
		b.WriteString("\n")
		b.WriteString(appearanceErrorStyle.Render("⚠ couldn't save theme: " + p.saveErr.Error()))
		b.WriteString("\n")
	}
	return b.String()
}

func indexOfTheme(value string) int {
	for i, opt := range appearanceThemeChoices {
		if opt.value == value {
			return i
		}
	}
	return 0
}

var (
	appearanceSectionStyle  = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Accent).Bold(true)
	appearanceRuleStyle     = lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim).Faint(true)
	appearanceCursorStyle   = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Accent).Bold(true)
	appearanceLabelStyle    = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Text)
	appearanceDescStyle     = lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim)
	appearanceCurrentStyle  = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Success)
	appearanceRadioOnStyle  = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Success)
	appearanceRadioOffStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim)
	appearanceErrorStyle    = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Error)
)
