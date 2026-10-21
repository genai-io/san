// /settings Appearance panel: three preference groups navigated as one list.
// Theme and thinking use radio choices; the status bars use compact toggles.
//
//   - COLOR THEME — light / dark / auto. Applied live and persisted to the
//     user-level settings file.
//   - STATUS BAR — independent context-usage and Git-information toggles.
//     Both default off; left/right previews Off/On, Enter applies the row.
//   - THINKING — full / collapsed / hidden. How the model's reasoning is
//     drawn. Full by default (the reasoning streams as before); collapsed
//     drops the body everywhere and shows one "∙ Thought for 3.2 s" line
//     instead.
//
// All are personal preferences, so — unlike Self-Learning — they have no
// project scope; selecting a row persists to the user settings file.
package input

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

// ContextBarSavedMsg is emitted after the appearance panel persists the
// context-bar on/off choice so the app can update its live display flag,
// refresh its settings handle, and show a confirmation. The value is already
// written to disk by the time this fires.
type ContextBarSavedMsg struct {
	On bool
}

// GitBarSavedMsg is emitted after persisting the Git-bar on/off choice.
type GitBarSavedMsg struct {
	On bool
}

// ThinkingDisplaySavedMsg is emitted after the appearance panel persists the
// reasoning-display choice so the app can update its live render flag, refresh
// its settings handle, and show a confirmation. The value is already written to
// disk by the time this fires.
type ThinkingDisplaySavedMsg struct {
	Mode string
}

// appearanceKind tags which setting a row applies when selected.
type appearanceKind int

const (
	kindTheme appearanceKind = iota
	kindContextBar
	kindGitBar
	kindThinkingDisplay
)

// appearanceOption is one selectable row. section heads the group it belongs
// to (printed once, above the group's first row). Exactly one value field is
// meaningful, selected by kind: theme for kindTheme, barOn for either bar kind,
// thinkingDisplay for kindThinkingDisplay.
type appearanceOption struct {
	section         string
	kind            appearanceKind
	label           string
	desc            string
	theme           string // kindTheme: the theme value to apply
	barOn           bool   // kindContextBar / kindGitBar: the on/off value to apply
	thinkingDisplay string // kindThinkingDisplay: the display mode to apply
}

// appearanceOptions is the full, section-ordered row list. The theme group
// comes first so the cursor can park on the current theme at Enter (and so
// indexOfTheme returns the same indices the first-run selector uses).
func appearanceOptions() []appearanceOption {
	return []appearanceOption{
		{section: "COLOR THEME", kind: kindTheme, label: "Dark", desc: "Dark background terminal", theme: "dark"},
		{section: "COLOR THEME", kind: kindTheme, label: "Light", desc: "Light background terminal", theme: "light"},
		{section: "COLOR THEME", kind: kindTheme, label: "Auto", desc: "Match terminal appearance automatically", theme: "auto"},
		{section: "STATUS BAR", kind: kindContextBar, label: "Context usage", desc: "Visual context meter"},
		{section: "STATUS BAR", kind: kindGitBar, label: "Git information", desc: "Branch, repository and changes"},
		{section: "THINKING", kind: kindThinkingDisplay, label: "Full", desc: "Draw the reasoning as it streams", thinkingDisplay: setting.ThinkingDisplayFull},
		{section: "THINKING", kind: kindThinkingDisplay, label: "Collapsed", desc: "One line, \"Thought for 3.2 s\" — no reasoning body", thinkingDisplay: setting.ThinkingDisplayCollapsed},
		{section: "THINKING", kind: kindThinkingDisplay, label: "Hidden", desc: "Draw nothing at all, not even \"Thought for 3.2 s\"", thinkingDisplay: setting.ThinkingDisplayHidden},
	}
}

type appearancePanel struct {
	settings *setting.Settings

	options []appearanceOption
	cursor  int // hovered row in options

	// Baselines are the values persisted on disk, marked "● current" in their
	// group. A group is "dirty" while the cursor hovers a row that diverges
	// from its baseline.
	themeBaseline    string
	barBaseline      bool
	gitBarBaseline   bool
	thinkingBaseline string

	// saveErr holds the last failed persist so Render can surface it inline
	// instead of silently swallowing it. Cleared on navigation / re-entry.
	saveErr error
}

func newAppearancePanel(settings *setting.Settings) *appearancePanel {
	return &appearancePanel{settings: settings}
}

func (p *appearancePanel) Title() string { return "appearance" }

func (p *appearancePanel) Enter() {
	p.options = appearanceOptions()
	p.themeBaseline = "auto"
	p.barBaseline = false
	p.gitBarBaseline = false
	p.thinkingBaseline = setting.DefaultThinkingDisplay
	if p.settings != nil {
		if data := p.settings.Snapshot(); data != nil {
			if data.Theme != "" {
				p.themeBaseline = data.Theme
			}
			p.barBaseline = data.ShowContextBar()
			p.gitBarBaseline = data.ShowGitBar()
			p.thinkingBaseline = data.ThinkingDisplayMode()
		}
	}
	p.resetBarChoices()
	p.cursor = indexOfTheme(p.themeBaseline)
	p.saveErr = nil
}

// Dirty reports whether the hovered row diverges from its group's saved
// value, so the shell pins the "● unsaved" indicator.
func (p *appearancePanel) Dirty() bool {
	return !p.isCurrent(p.options[p.cursor])
}

func (p *appearancePanel) HandleKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "up", "k":
		if p.cursor > 0 {
			p.resetBarChoices()
			p.cursor--
		}
		p.saveErr = nil
	case "down", "j":
		if p.cursor < len(p.options)-1 {
			p.resetBarChoices()
			p.cursor++
		}
		p.saveErr = nil
	case "left", "h", "right", "l":
		if isAppearanceBar(p.options[p.cursor]) {
			p.options[p.cursor].barOn = msg.String() == "right" || msg.String() == "l"
			p.saveErr = nil
		}
	case "enter", " ":
		return p.apply(p.options[p.cursor])
	}
	return nil, false
}

// apply persists (and, for the theme, live-applies) the hovered option,
// advances that group's baseline, and returns the confirmation command.
// On failure it sets saveErr, keeps the popup open (done=false), and leaves
// the baseline untouched.
func (p *appearancePanel) apply(opt appearanceOption) (tea.Cmd, bool) {
	switch opt.kind {
	case kindTheme:
		// Apply live so the switch is visible immediately. InitTheme only
		// flips lipgloss's cached background flag (no terminal I/O), so it is
		// safe to call mid-program.
		kit.InitTheme(opt.theme)
		if err := setting.SaveTheme(opt.theme); err != nil {
			// Keep the on-screen theme in sync with what's actually persisted:
			// revert the live apply and surface the error instead of leaving
			// the UI showing a theme that won't survive a restart.
			kit.InitTheme(p.themeBaseline)
			p.saveErr = err
			return nil, false
		}
		p.themeBaseline = opt.theme
		return func() tea.Msg { return ThemeSavedMsg{Theme: opt.theme} }, true
	case kindContextBar:
		if err := setting.SaveContextBar(opt.barOn); err != nil {
			p.saveErr = err
			return nil, false
		}
		p.barBaseline = opt.barOn
		return func() tea.Msg { return ContextBarSavedMsg{On: opt.barOn} }, true
	case kindGitBar:
		if err := setting.SaveGitBar(opt.barOn); err != nil {
			p.saveErr = err
			return nil, false
		}
		p.gitBarBaseline = opt.barOn
		return func() tea.Msg { return GitBarSavedMsg{On: opt.barOn} }, true
	case kindThinkingDisplay:
		if err := setting.SaveThinkingDisplay(opt.thinkingDisplay); err != nil {
			p.saveErr = err
			return nil, false
		}
		p.thinkingBaseline = opt.thinkingDisplay
		return func() tea.Msg { return ThinkingDisplaySavedMsg{Mode: opt.thinkingDisplay} }, true
	}
	return nil, false
}

func (p *appearancePanel) HintLine() string {
	if isAppearanceBar(p.options[p.cursor]) {
		return keycap("↑↓") + " move  " + keycap("←→") + " off/on  " + keycap("enter") + " apply"
	}
	return radioHintLine()
}

// renderAppearanceSection renders a section heading followed by a rule that
// fills the remaining width.
func renderAppearanceSection(title string, width int) string {
	ruleLen := max(width-lipgloss.Width(title)-1, 1)
	return appearanceSectionStyle.Render(title) + " " + appearanceRuleStyle.Render(strings.Repeat("─", ruleLen))
}

// renderRadioRow renders one selectable row of a /settings radio group: the
// hover caret, the ○/● radio, a label padded to a common column so the
// descriptions line up, and the "current" tag on the persisted choice.
// Shared by every panel with a radio group so the tabs cannot drift apart.
func renderRadioRow(label, desc string, hovered, current bool) string {
	caret := "  "
	rendered := appearanceLabelStyle.Render(label)
	if hovered {
		caret = appearanceCursorStyle.Render("▸ ")
		rendered = appearanceCursorStyle.Render(label)
	}

	radio := appearanceRadioOffStyle.Render("○")
	tag := ""
	if current {
		radio = appearanceRadioOnStyle.Render("●")
		tag = "  " + appearanceCurrentStyle.Render("current")
	}

	labelCell := rendered + strings.Repeat(" ", max(10-lipgloss.Width(label), 1))
	return caret + radio + " " + labelCell + appearanceDescStyle.Render(desc) + tag
}

// radioHintLine is the shared bottom hint for a radio-group panel.
func radioHintLine() string {
	return keycap("↑↓") + " move  " + keycap("enter") + " apply"
}

// isCurrent reports whether opt matches the persisted value for its group.
func (p *appearancePanel) isCurrent(opt appearanceOption) bool {
	switch opt.kind {
	case kindContextBar:
		return opt.barOn == p.barBaseline
	case kindGitBar:
		return opt.barOn == p.gitBarBaseline
	case kindThinkingDisplay:
		return opt.thinkingDisplay == p.thinkingBaseline
	default:
		return opt.theme == p.themeBaseline
	}
}

// indexOfTheme returns the row index of the given theme value, or 0 (the
// first row) when it is unknown / unset.
func indexOfTheme(value string) int {
	for i, opt := range appearanceOptions() {
		if opt.kind == kindTheme && opt.theme == value {
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
