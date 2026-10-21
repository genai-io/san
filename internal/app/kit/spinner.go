package kit

import "time"

// LiveDotFPS is the frame interval every live indicator advances on.
const LiveDotFPS = 360 * time.Millisecond

// LiveDot is the one indicator for anything still in flight: a dim ● that
// blinks out one frame in four (on 1.08s, off 0.36s at LiveDotFPS). The row
// settles into a solid ● in its own color when the work is done, so the
// color says done and the blink says waiting.
func LiveDot(frame int) string {
	if frame%4 == 3 {
		return " "
	}
	return DimStyle().Render("●")
}
