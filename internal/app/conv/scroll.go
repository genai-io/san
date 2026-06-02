// Vertical scroll viewport for the chat content area: a pure helper that
// windows a block of lines around a scroll offset and decorates it with
// "more above" / "more below" indicators.
package conv

import (
	"fmt"
	"strings"
)

// ScrollWindow renders a vertical viewport into content lines, showing at most
// maxHeight rows (content rows plus any scroll indicators).
//
// offset is how many lines the viewport is scrolled up from the bottom:
//   - offset <= 0 pins the view to the bottom (latest content), no indicators.
//   - offset > 0 shows an earlier window with "↑ N more lines above" and
//     "↓ N lines below" indicators; the indicators count toward maxHeight so
//     the result never exceeds maxHeight rows.
//
// It returns the rendered window and the offset clamped to the valid range.
// Callers should store the returned offset back as the canonical scroll
// position — this is the single place scroll bounds are enforced, so the
// stored offset can never run past the top of the content.
func ScrollWindow(lines []string, offset, maxHeight int) (string, int) {
	if maxHeight <= 0 {
		return strings.Join(lines, "\n"), offset
	}

	total := len(lines)
	if total <= maxHeight {
		// Everything fits; pin to the bottom, nothing to scroll.
		return strings.Join(lines, "\n"), 0
	}

	if offset <= 0 {
		// At the bottom: show the latest maxHeight lines, no indicators.
		return strings.Join(lines[total-maxHeight:], "\n"), 0
	}

	// Scrolled up. Clamp so the window never scrolls past the first line
	// (at which point line 0 sits at the top of the window).
	maxOffset := total - maxHeight + 1
	if offset > maxOffset {
		offset = maxOffset
	}

	// Decide how many content rows fit. We always reserve one row for the
	// "↓ below" indicator; if any content remains above the window we reserve
	// a second row for the "↑ above" indicator. Deciding "above" from the
	// tentative top (with avail = maxHeight-1) keeps the total at maxHeight —
	// reserving on the final top index, not an off-by-one estimate.
	avail := maxHeight - 1
	top := total - offset - avail
	if top > 0 {
		avail--
		top = total - offset - avail
	}
	if top < 0 {
		top = 0
	}
	bottom := total - offset

	var sb strings.Builder
	if top > 0 {
		sb.WriteString(ThinkingStyle.Render(fmt.Sprintf("↑ %d more lines above", top)))
		sb.WriteString("\n")
	}
	sb.WriteString(strings.Join(lines[top:bottom], "\n"))
	sb.WriteString("\n")
	sb.WriteString(ThinkingStyle.Render(fmt.Sprintf("↓ %d lines below (wheel down to return)", offset)))

	return sb.String(), offset
}
