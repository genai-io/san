package kit

import (
	"time"

	"charm.land/lipgloss/v2"
)

// LiveDotFPS is the frame interval every live indicator advances on.
const LiveDotFPS = 360 * time.Millisecond

// Live marks breathe instead of blinking: each breath is one color per frame,
// up from near the background to a peak and back, so every live mark moves in
// step and none drops out.
var (
	// The dot holds near the background, swells to the brightest text color,
	// holds, and fades, so the swing reads in either theme.
	dotFaint  = AdaptiveColor{Dark: "#232327", Light: "#F4F4F5"}
	dotMid    = AdaptiveColor{Dark: "#8F8F90", Light: "#7F7F80"}
	dotBreath = [...]AdaptiveColor{dotFaint, dotFaint, dotMid, CurrentTheme.TextBright, CurrentTheme.TextBright, dotMid}

	// The star peaks at body text.
	starFaint  = AdaptiveColor{Dark: "#3F3F46", Light: "#E4E4E7"}
	starMid    = AdaptiveColor{Dark: "#6E7480", Light: "#A1A1AA"}
	starBreath = [...]AdaptiveColor{starFaint, starMid, CurrentTheme.TextDim, CurrentTheme.Text, CurrentTheme.TextDim, starMid}
)

// LiveDot is the one indicator for a reply or action still in flight: a
// breathing ●. The row settles into a solid ● in its own color when the work
// is done, so the color says done and the breathing says waiting.
func LiveDot(frame int) string {
	return liveDotMark("●", frame)
}

// MemoryMark identifies a memory being read or supplied as context.
const MemoryMark = "⟡"

// LiveMemory gives an in-flight memory read the same gentle breath as tools.
func LiveMemory(frame int) string {
	return liveDotMark(MemoryMark, frame)
}

func liveDotMark(mark string, frame int) string {
	return lipgloss.NewStyle().Foreground(dotBreath[frame%len(dotBreath)]).Render(mark)
}

// ThinkingMark leads reasoning: the star of the San mark, apart from the
// reply's ●.
const ThinkingMark = "✦"

// LiveStar is the breathing ThinkingMark held while the model's reply is
// awaited, bold so the small glyph reads.
func LiveStar(frame int) string {
	return lipgloss.NewStyle().Bold(true).Foreground(starBreath[frame%len(starBreath)]).Render(ThinkingMark)
}
