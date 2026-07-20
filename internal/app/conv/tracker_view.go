package conv

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/todo"
)

// maxVisibleItems caps the rows drawn. The window sits on the newest items,
// not the oldest, because the list outlives the turn that filled it (see
// OnTurnEnd): taking from the front would pin the panel to the turn that
// stalled and hide the work in flight.
const maxVisibleItems = 8

// trackerPulseTicks is the number of spinner frames per ●/◌ swap of an
// in-progress item. At ~360ms per frame this gives a ~1.1s breathe — calmer
// than the agent icon's faster blink (cf. agentBlinkTicks in tool_render.go),
// suiting the tracker's quieter role.
const trackerPulseTicks = 3

// TrackerListParams holds the parameters for rendering a tracker list.
type TrackerListParams struct {
	Items        []*todo.Item
	StreamActive bool
	Width        int
	Blockers     func(itemID string) []string
	// Executing reports whether the executor behind an item is running right
	// now. An in_progress item only animates when Executing says so: the
	// status field records intent and outlives the process that wrote it, so
	// it cannot by itself justify a moving pixel.
	Executing func(t *todo.Item) bool
	// Blink is the shared frame-tick counter (see FrameClock.Frame) that
	// drives the in-progress pulse via trackerPulseTicks.
	Blink int
}

// RenderTrackerList renders the tracker panel above the input area.
// Returns empty string when there are no items, or all are completed and idle.
func RenderTrackerList(params TrackerListParams) string {
	if len(params.Items) == 0 {
		return ""
	}

	ended := 0
	for _, t := range params.Items {
		if t.Status == todo.StatusCompleted {
			ended++
		}
	}

	// A fully closed-out list has nothing left to track, so the panel gets out
	// of the way once the stream is idle. While streaming it stays up: the model
	// can still add items, and a list that vanished mid-turn would flicker back.
	//
	// Derived from the items in hand rather than asked of the store: the count
	// above already answers it, and one snapshot cannot disagree with itself.
	if ended == len(params.Items) && !params.StreamActive {
		return ""
	}

	var sb strings.Builder
	headerStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim).Bold(true)
	mutedStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	sb.WriteString("  " + headerStyle.Render("Tasks") + " " +
		mutedStyle.Render(fmt.Sprintf("(%d%%)", ended*100/len(params.Items))))
	sb.WriteString("\n")

	visible := params.Items[max(0, len(params.Items)-maxVisibleItems):]
	if hidden := len(params.Items) - len(visible); hidden > 0 {
		// Name the drop; a silent truncation reads as the whole list.
		sb.WriteString("  " + overflowStyle.Render(fmt.Sprintf("+%d more above", hidden)) + "\n")
	}

	idWidth := itemIDWidth(visible)

	// Classify only the rows actually drawn. Liveness is the expensive input and
	// the header above does not need it — progress counts finished work, which
	// no worker can still be advancing.
	for _, t := range visible {
		phase := phaseOf(t, params.Executing != nil && params.Executing(t))
		sb.WriteString(renderItem(t, phase, params.Width, idWidth, params.Blockers, params.Blink))
	}

	return sb.String()
}

// itemPhase is how an item reads to the user. It collapses the recorded status,
// how a finished item ended, and whether an executor is actually running it
// into one exhaustive set, so each render branch answers to exactly one phase.
type itemPhase int

const (
	itemWaiting  itemPhase = iota // nothing has started it
	itemRunning                   // in progress, with a live executor
	itemStalled                   // marked in progress, but nothing is executing it
	itemAborted                   // ended without finishing its work
	itemFinished                  // ended having done its work
)

// phaseOf classifies an item. executing answers whether its executor is running;
// it only distinguishes itemRunning from itemStalled, since an item that already
// reached a terminal status has no worker left to ask about.
func phaseOf(t *todo.Item, executing bool) itemPhase {
	switch t.Status {
	case todo.StatusCompleted:
		if todo.EndedAbnormally(t) {
			return itemAborted
		}
		return itemFinished
	case todo.StatusInProgress:
		if executing {
			return itemRunning
		}
		return itemStalled
	default:
		return itemWaiting
	}
}

// activeText prefers the item's active phrasing ("Auditing deps") over its
// subject ("Audit deps") while it is the one being worked on.
func activeText(t *todo.Item, maxTextLen int) string {
	text := t.ActiveForm
	if text == "" {
		text = t.Subject
	}
	return kit.TruncateText(text, maxTextLen)
}

func renderItem(t *todo.Item, phase itemPhase, width, idWidth int, blockers func(string) []string, blink int) string {
	indent := "  "
	idTag := fmt.Sprintf("%-*s", idWidth, "#"+t.ID)
	maxTextLen := max(width-len(indent)-idWidth-8, 12)
	subject := kit.TruncateText(t.Subject, maxTextLen)
	mutedStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)

	switch phase {
	case itemAborted:
		abortedStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Error)
		detail := mutedStyle.Render("[" + string(todo.BackgroundStatusDetail(t)) + "]")
		return renderItemLine(indent, abortedStyle.Render("!"), idTag, subject, detail)

	case itemFinished:
		return renderItemLine(indent, trackerCompletedStyle.Render("●"), idTag, subject, "")

	case itemStalled:
		// Nothing is executing this item, so draw it at rest. Reaching here
		// means the status outlived its executor within a live session — the
		// model marked an item in_progress and moved on without closing it.
		// Animating would claim work that isn't happening.
		return renderItemLine(indent, mutedStyle.Render("◌"), idTag, activeText(t, maxTextLen), mutedStyle.Render("[stalled]"))

	case itemRunning:
		// Pulse on the shared frame tick (a true ~360ms clock; see FrameClock)
		// rather than the wall clock, which only sampled on redraws and so
		// flickered irregularly.
		activeIcon := "●"
		activeStyle := trackerInProgressStyle
		if (blink/trackerPulseTicks)%2 == 1 {
			activeIcon = "◌"
			activeStyle = mutedStyle
		}
		detail := ""
		if elapsed := formatElapsedTime(t.StatusChangedAt); elapsed != "" {
			detail = mutedStyle.Render(elapsed)
		}
		return renderItemLine(indent, activeStyle.Render(activeIcon), idTag, activeText(t, maxTextLen), detail)

	default:
		detail := ""
		if blockers != nil {
			if bl := blockers(t.ID); len(bl) > 0 {
				blockerRefs := make([]string, len(bl))
				for i, b := range bl {
					blockerRefs[i] = "#" + b
				}
				blockedStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Error)
				detail = blockedStyle.Render("← " + strings.Join(blockerRefs, ", "))
			}
		}
		return renderItemLine(indent, trackerPendingStyle.Render("○"), idTag, subject, detail)
	}
}

func renderItemLine(indent, icon, id, subject, detail string) string {
	line := indent + icon + "  " + id + "  " + subject
	if detail != "" {
		line += "  " + detail
	}
	return line + "\n"
}

func itemIDWidth(items []*todo.Item) int {
	width := 2
	for _, t := range items {
		if n := len("#" + t.ID); n > width {
			width = n
		}
	}
	return width
}

func formatElapsedTime(since time.Time) string {
	if since.IsZero() {
		return ""
	}
	d := time.Since(since)
	if d < time.Second {
		return ""
	}
	minutes := int(d.Minutes())
	seconds := int(d.Seconds()) % 60
	if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}
