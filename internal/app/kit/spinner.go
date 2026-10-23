package kit

import (
	"time"

	"charm.land/lipgloss/v2"
)

// LiveDotFPS is the frame interval every live indicator advances on.
const LiveDotFPS = 360 * time.Millisecond

// Live marks breathe instead of blinking: each frame steps the color up from
// near the background to a peak and back, so every live mark moves in step and
// none drops out. The reasoning star peaks at body text.
var (
	pulseFaint = AdaptiveColor{Dark: "#3F3F46", Light: "#E4E4E7"}
	pulseLow   = AdaptiveColor{Dark: "#6E7480", Light: "#A1A1AA"}

	// The dot holds near the background, swells to the brightest text color,
	// holds, and fades: the full contrast range, paused at both ends so the
	// swing reads in either theme.
	dotTrough = AdaptiveColor{Dark: "#232327", Light: "#F4F4F5"}
	dotMid    = AdaptiveColor{Dark: "#8F8F90", Light: "#7F7F80"}
	dotPulse  = [...]AdaptiveColor{dotTrough, dotTrough, dotMid, CurrentTheme.TextBright, CurrentTheme.TextBright, dotMid}
	starPulse = [...]AdaptiveColor{pulseFaint, pulseLow, CurrentTheme.TextDim, CurrentTheme.Text, CurrentTheme.TextDim, pulseLow}
)

// LiveDot is the one indicator for a reply or action still in flight: a
// breathing ●. The row settles into a solid ● in its own color when the work
// is done, so the color says done and the breathing says waiting.
func LiveDot(frame int) string {
	return lipgloss.NewStyle().Foreground(dotPulse[frame%len(dotPulse)]).Render("●")
}

// LiveStar is the breathing ✦ that marks a wait on the model's reply, bold so
// the small glyph reads.
func LiveStar(frame int) string {
	return lipgloss.NewStyle().Bold(true).Foreground(starPulse[frame%len(starPulse)]).Render("✦")
}
