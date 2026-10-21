package kit

import "time"

// LiveDotFPS is the frame interval every live indicator advances on.
const LiveDotFPS = 360 * time.Millisecond

// BlinkOn reports whether a live mark shows on this frame: it blinks out one
// frame in four (on 1.08s, off 0.36s at LiveDotFPS). Every live mark reads
// its phase here, so all of them blink in step.
func BlinkOn(frame int) bool { return frame%4 != 3 }

// LiveDot is the one indicator for a reply or action still in flight: a dim
// ● that blinks. The row settles into a solid ● in its own color when the
// work is done, so the color says done and the blink says waiting.
func LiveDot(frame int) string {
	if !BlinkOn(frame) {
		return " "
	}
	return DimStyle().Render("●")
}
