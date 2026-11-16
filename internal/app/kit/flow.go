package kit

import "charm.land/lipgloss/v2"

// FlowLabel gives Flow presentations a neutral identity, independent of task IDs.
func FlowLabel(title string) string {
	body := lipgloss.NewStyle().Foreground(CurrentTheme.Text)
	label := body.Bold(true).Render("Flow")
	if title != "" {
		label += body.Render(" · " + title)
	}
	return label
}
