package input

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/app/kit"
)

func isAppearanceBar(opt appearanceOption) bool {
	return opt.kind == kindContextBar || opt.kind == kindGitBar
}

// resetBarChoices initializes the compact toggles from the saved preferences.
// Leaving a row discards its un-applied preview, just like leaving a radio
// choice: Enter always applies only the focused preference.
func (p *appearancePanel) resetBarChoices() {
	for i := range p.options {
		switch p.options[i].kind {
		case kindContextBar:
			p.options[i].barOn = p.barBaseline
		case kindGitBar:
			p.options[i].barOn = p.gitBarBaseline
		}
	}
}

// renderBarToggle shares a label column and compact Off / On cells between
// both preferences. Only the selected value has a background, not the row.
// Unfocused controls use a neutral fill; the focused value shares the popup's
// active-tab accent so color marks navigation, not every enabled preference.
func renderBarToggle(opt appearanceOption, hovered bool, width int) string {
	caret := "  "
	label := appearanceLabelStyle.Render(opt.label)
	if hovered {
		caret = appearanceCursorStyle.Render("▸ ")
		label = appearanceCursorStyle.Render(opt.label)
	}
	label += strings.Repeat(" ", max(18-lipgloss.Width(opt.label), 1))

	offStyle, onStyle := appearanceToggleStyle, appearanceToggleStyle
	selected := appearanceToggleSelectedStyle
	if hovered {
		selected = appearanceToggleFocusedStyle
	}
	if opt.barOn {
		onStyle = selected
	} else {
		offStyle = selected
	}
	row := caret + label + offStyle.Render("Off") + " " + onStyle.Render("On")
	if remaining := width - lipgloss.Width(row) - 2; remaining >= 16 {
		row += "  " + appearanceDescStyle.Render(ansi.Truncate(opt.desc, remaining, "…"))
	}
	return row
}

// Adaptive colors resolve at render time, so these shared styles still track
// live theme changes without rebuilding them for every toggle and frame.
var (
	appearanceToggleStyle         = lipgloss.NewStyle().Width(5).Align(lipgloss.Center).Foreground(kit.CurrentTheme.TextDim)
	appearanceToggleSelectedStyle = appearanceToggleStyle.Background(kit.CurrentTheme.Border).Foreground(kit.CurrentTheme.Text).Bold(true)
	appearanceToggleFocusedStyle  = appearanceToggleSelectedStyle.Background(kit.CurrentTheme.Focus).Foreground(kit.CurrentTheme.Background)
)
