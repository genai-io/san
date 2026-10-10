package conv

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/todo"
)

// maxVisibleItems caps task rows; overflow is summarized in one extra row.
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
	// AgentColors maps an agent's name to its display color. An item owned by a
	// background agent (Owner set) is tinted with that color, matching the
	// agent's launch line in the flow. Plain user todos have no owner and keep
	// the status palette.
	AgentColors map[string]string
}

// RenderTrackerList renders the tracker panel above the input area.
// Returns empty string when there are no items, or all are completed and idle.
func RenderTrackerList(params TrackerListParams) string {
	if len(params.Items) == 0 {
		return ""
	}

	ended, plans, plansDone := 0, 0, 0
	for _, t := range params.Items {
		done := t.Status == todo.StatusCompleted
		if done {
			ended++
		}
		if todo.BackgroundTaskID(t) == "" {
			plans++
			if done {
				plansDone++
			}
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
	// Progress is the plan's: a background task finishing is not a step of it.
	if plans > 0 {
		sb.WriteString("  " + headerStyle.Render("Tasks") + " " +
			mutedStyle.Render(fmt.Sprintf("(%d%%)", plansDone*100/plans)))
	} else {
		sb.WriteString("  " + headerStyle.Render("Background"))
	}
	sb.WriteString("\n")

	visible, hidden := visibleTrackerItems(params.Items)
	sb.WriteString(renderFoldedLine(hidden))

	idWidth := itemIDWidth(visible)

	// Query executor liveness only for visible rows; counts use recorded status.
	for _, t := range visible {
		phase := phaseOf(t, params.Executing != nil && params.Executing(t))
		sb.WriteString(renderItem(t, phase, params.Width, idWidth, params.Blockers, params.Blink, params.AgentColors))
	}

	return sb.String()
}

// visibleTrackerItems keeps the newest items in each priority group, then draws
// them in their original order. Successful completions yield space first.
func visibleTrackerItems(items []*todo.Item) ([]*todo.Item, [itemFinished + 1]int) {
	var hidden [itemFinished + 1]int
	if len(items) <= maxVisibleItems {
		return items, hidden
	}
	for _, t := range items {
		hidden[phaseOf(t, false)]++
	}
	remaining := maxVisibleItems
	for _, phase := range []itemPhase{itemStalled, itemAborted, itemWaiting, itemFinished} {
		shown := min(hidden[phase], remaining)
		hidden[phase] -= shown
		remaining -= shown
	}

	skip := hidden
	visible := make([]*todo.Item, 0, maxVisibleItems)
	for _, t := range items {
		phase := phaseOf(t, false)
		if skip[phase] > 0 {
			skip[phase]--
			continue
		}
		visible = append(visible, t)
	}
	return visible, hidden
}

func renderFoldedLine(hidden [itemFinished + 1]int) string {
	var parts []string
	for _, group := range []struct {
		phase itemPhase
		label string
	}{
		{itemStalled, "more in progress"},
		{itemAborted, "failed/stopped"},
		{itemWaiting, "more pending"},
		{itemFinished, "completed"},
	} {
		if n := hidden[group.phase]; n > 0 {
			text := fmt.Sprintf("%d %s", n, group.label)
			if group.phase == itemAborted {
				text = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Error).Render(text)
			}
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	mutedStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	return "  " + mutedStyle.Render("…  "+strings.Join(parts, ", ")) + "\n"
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

func renderItem(t *todo.Item, phase itemPhase, width, idWidth int, blockers func(string) []string, blink int, agentColors map[string]string) string {
	indent := "  "
	mutedStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	idTag := fmt.Sprintf("%-*s", idWidth, rowTag(t))
	if todo.BackgroundTaskID(t) != "" {
		idTag = mutedStyle.Render(idTag)
	}
	maxTextLen := max(width-len(indent)-idWidth-8, 12)
	subject := kit.TruncateText(t.Subject, maxTextLen)

	// A row owned by a background agent wears that agent's color (icon + text),
	// mirroring its launch line in the flow; a plain todo keeps the status
	// palette. Aborted rows stay error-red and stalled rows stay muted — those
	// states carry more meaning than whose agent produced them.
	agentFg, owned := agentForeground(t, agentColors)
	tint := func(s lipgloss.Style) lipgloss.Style {
		if owned {
			return s.Foreground(agentFg)
		}
		return s
	}

	switch phase {
	case itemAborted:
		abortedStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Error)
		detail := mutedStyle.Render("[" + todo.BackgroundStatusDetail(t) + "]")
		return renderItemLine(indent, abortedStyle.Render("!"), idTag, subject, detail)

	case itemFinished:
		return renderItemLine(indent, tint(trackerCompletedStyle).Render("●"), idTag, tint(lipgloss.NewStyle()).Render(subject), "")

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
		activeStyle := tint(trackerInProgressStyle)
		if (blink/trackerPulseTicks)%2 == 1 {
			activeIcon = "◌"
			activeStyle = mutedStyle
		}
		detail := ""
		if elapsed := formatElapsedTime(t.StatusChangedAt); elapsed != "" {
			detail = mutedStyle.Render(elapsed)
		}
		return renderItemLine(indent, activeStyle.Render(activeIcon), idTag, tint(lipgloss.NewStyle()).Render(activeText(t, maxTextLen)), detail)

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
		return renderItemLine(indent, tint(trackerPendingStyle).Render("○"), idTag, tint(lipgloss.NewStyle()).Render(subject), detail)
	}
}

// agentForeground returns the owning agent's display color for an item, and
// whether the item is owned by a colored agent at all — a plain user todo has no
// owner, and an owner with no configured color resolves to nothing.
func agentForeground(t *todo.Item, agentColors map[string]string) (kit.AdaptiveColor, bool) {
	if t.Owner == "" || len(agentColors) == 0 {
		return kit.AdaptiveColor{}, false
	}
	color, ok := agentColors[strings.ToLower(t.Owner)]
	if !ok || color == "" {
		return kit.AdaptiveColor{}, false
	}
	return agentColor(color), true
}

func renderItemLine(indent, icon, id, subject, detail string) string {
	line := indent + icon + "  " + id + "  " + subject
	if detail != "" {
		line += "  " + detail
	}
	return line + "\n"
}

// rowTag leads a row: "#ID" for a plan item, the task kind for a background
// task, whose tracker ID is no handle the user can act on.
func rowTag(t *todo.Item) string {
	if todo.BackgroundTaskID(t) == "" {
		return "#" + t.ID
	}
	if kind := todo.BackgroundTaskType(t); kind != "" {
		return kind
	}
	return "bg"
}

func itemIDWidth(items []*todo.Item) int {
	width := 2
	for _, t := range items {
		width = max(width, len(rowTag(t)))
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
