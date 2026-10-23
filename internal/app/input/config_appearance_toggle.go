package input

import (
	"strconv"
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
		case kindBashPreview:
			p.options[i].lines = p.previewBaseline
		}
	}
}

// renderBarToggle shares a label column and compact Off / On cells between
// both preferences. Only the selected value has a background, not the row.
// Unfocused controls use a neutral fill; the focused value shares the popup's
// active-tab accent so color marks navigation, not every enabled preference.
func renderBarToggle(opt appearanceOption, hovered bool, width int) string {
	head, selected := appearanceRowHead(opt, hovered)
	offStyle, onStyle := appearanceToggleStyle, appearanceToggleStyle
	if opt.barOn {
		onStyle = selected
	} else {
		offStyle = selected
	}
	return withDesc(head+offStyle.Render("Off")+" "+onStyle.Render("On"), opt.desc, width)
}

// renderLineStepper draws the preview-lines row in the toggles' layout: the
// count in one cell between ‹ › arrows, "Off" for 0.
func renderLineStepper(opt appearanceOption, hovered bool, width int) string {
	head, selected := appearanceRowHead(opt, hovered)
	value := "Off"
	if opt.lines > 0 {
		value = strconv.Itoa(opt.lines)
	}
	arrow := appearanceDescStyle.Render
	return withDesc(head+arrow("‹ ")+selected.Render(value)+arrow(" ›"), opt.desc, width)
}

// appearanceRowHead is a control row's caret and padded label, plus the style
// of its selected value: the active-tab accent while the row has the cursor.
func appearanceRowHead(opt appearanceOption, hovered bool) (string, lipgloss.Style) {
	caret, label, selected := "  ", appearanceLabelStyle.Render(opt.label), appearanceToggleSelectedStyle
	if hovered {
		caret, label, selected = appearanceCursorStyle.Render("▸ "), appearanceCursorStyle.Render(opt.label), appearanceToggleFocusedStyle
	}
	return caret + label + strings.Repeat(" ", max(18-lipgloss.Width(opt.label), 1)), selected
}

// withDesc appends the row's description when it fits.
func withDesc(row, desc string, width int) string {
	if remaining := width - lipgloss.Width(row) - 2; remaining >= 16 {
		row += "  " + appearanceDescStyle.Render(ansi.Truncate(desc, remaining, "…"))
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
