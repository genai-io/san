package kit

import "charm.land/lipgloss/v2"

// RecapMark is the progress mark for a recap being generated.
const RecapMark = "※"

// LiveRecap changes only the mark's tone; its shape stays fixed on every frame.
func LiveRecap(frame int) string {
	return lipgloss.NewStyle().Foreground(starBreath[frame%len(starBreath)]).Render(RecapMark)
}
