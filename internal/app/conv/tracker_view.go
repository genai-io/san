package conv

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/todo"
)

// maxVisibleItems caps task rows; overflow is summarized in one extra row.
const maxVisibleItems = 8

// TrackerListParams holds the parameters for rendering a tracker list.
type TrackerListParams struct {
	Items        []*todo.Item
	StreamActive bool
	Width        int
	Blockers     func(itemID string) []string
	// CompletionShown keeps a successful worker visible until its notice is shown.
	// Nil treats completions as already announced.
	CompletionShown func(taskID string) bool
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
	// WorkerStatus provides display-only live activity and output-log health.
	WorkerStatus func(taskID string) task.TaskInfo
}

// RenderTrackerList renders the tracker panel above the input area.
// Successful workers leave after their notice is shown; finished plans stay until idle.
func RenderTrackerList(params TrackerListParams) string {
	if len(params.Items) == 0 {
		return ""
	}

	items := make([]*todo.Item, 0, len(params.Items))
	for _, t := range params.Items {
		id := todo.BackgroundTaskID(t)
		if id != "" && phaseOf(t, false) == itemFinished && (params.CompletionShown == nil || params.CompletionShown(id)) {
			continue
		}
		items = append(items, t)
	}
	plansDone, plans := PlanProgress(params.Items)

	// Keep exceptions visible even when idle; only the plan waits for turn end.
	if len(items) == 0 || (plansDone == len(items) && !params.StreamActive) {
		return ""
	}

	var sb strings.Builder
	headerStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim).Bold(true)
	mutedStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	header := "  " + headerStyle.Render("Tasks")
	// Progress is the plan's: a background task finishing is not a step of it.
	if plans > 0 {
		header += " " + mutedStyle.Render(fmt.Sprintf("(%d%%)", plansDone*100/plans))
	}
	sb.WriteString(kit.TruncateText(header, max(1, params.Width)) + "\n")

	visible, hidden := visibleTrackerItems(items)
	sb.WriteString(renderFoldedLine(hidden))

	idWidth := itemIDWidth(visible)

	// Query executor liveness only for visible rows; counts use recorded status.
	for _, t := range visible {
		phase := phaseOf(t, params.Executing != nil && params.Executing(t))
		progress := ""
		if phase == itemAborted {
			progress = todo.BackgroundError(t)
		}
		if id := todo.BackgroundTaskID(t); id != "" && params.WorkerStatus != nil {
			info := params.WorkerStatus(id)
			if phase == itemAborted {
				// A final reason outranks stale activity (e.g. the last Read call).
				if info.Error != "" {
					progress = info.Error
				}
			} else {
				progress = info.Progress
				if progress == "" {
					// Structured callers may supply only LiveView.
					progress, _, _ = strings.Cut(info.LiveView, "\n")
				}
			}
			if info.OutputError != "" {
				if phase == itemAborted && progress != "" {
					progress += " · output log unavailable"
				} else {
					progress = "output log unavailable · " + progress
				}
			}
		}
		progress = strings.Join(strings.Fields(progress), " ")
		// Keep one row per worker. Activity must not double tracker height and
		// push sibling identities out of a short terminal.
		suffix := ""
		if progress != "" {
			suffix = mutedStyle.Render(kit.TruncateText(" · "+progress, max(1, params.Width/2)))
		}
		line := renderItem(t, phase, max(1, params.Width-lipgloss.Width(suffix)), idWidth, params.Blockers, params.Blink, params.AgentColors)
		sb.WriteString(kit.TruncateText(strings.TrimSuffix(line, "\n")+suffix, max(1, params.Width)) + "\n")
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

// PlanProgress counts the plan's items, leaving out background work, and how
// many of them finished.
func PlanProgress(items []*todo.Item) (done, total int) {
	for _, t := range items {
		if todo.BackgroundTaskID(t) == "" {
			total++
			if phaseOf(t, false) == itemFinished {
				done++
			}
		}
	}
	return done, total
}

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
func activeText(t *todo.Item) string {
	text := t.ActiveForm
	if text == "" {
		text = t.Subject
	}
	return text
}

func renderItem(t *todo.Item, phase itemPhase, width, idWidth int, blockers func(string) []string, blink int, agentColors map[string]string) string {
	indent := "  "
	mutedStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	idTag := ""
	if todo.BackgroundTaskID(t) == "" {
		idTag = fmt.Sprintf("%-*s", idWidth, "#"+t.ID)
	}
	subject := t.Subject

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
		return renderItemLine(indent, abortedStyle.Render("!"), idTag, subject, detail, width)

	case itemFinished:
		detail := ""
		if todo.BackgroundTaskID(t) != "" {
			detail = mutedStyle.Render("[completed]")
		}
		return renderItemLine(indent, tint(trackerCompletedStyle).Render("●"), idTag, tint(lipgloss.NewStyle()).Render(subject), detail, width)

	case itemStalled:
		// Nothing is executing this item, so draw it at rest. Reaching here
		// means the status outlived its executor within a live session — the
		// model marked an item in_progress and moved on without closing it.
		// Animating would claim work that isn't happening.
		return renderItemLine(indent, mutedStyle.Render("◌"), idTag, activeText(t), mutedStyle.Render("[stalled]"), width)

	case itemRunning:
		// Blink on the shared frame tick (a true ~360ms clock; see FrameClock)
		// rather than the wall clock, which only sampled on redraws and so
		// flickered irregularly.
		detail := ""
		if elapsed := formatElapsedTime(t.StatusChangedAt); elapsed != "" {
			detail = mutedStyle.Render("· " + elapsed)
		}
		return renderItemLine(indent, kit.LiveDot(blink), idTag, tint(lipgloss.NewStyle()).Render(activeText(t)), detail, width)

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
		return renderItemLine(indent, tint(trackerPendingStyle).Render("○"), idTag, tint(lipgloss.NewStyle()).Render(subject), detail, width)
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

func renderItemLine(indent, icon, id, subject, detail string, width int) string {
	prefix := indent + icon + "  "
	if id != "" {
		prefix += id + "  "
	}
	suffix := ""
	if detail != "" {
		suffix = "  " + detail
	}
	textWidth := width - lipgloss.Width(prefix+suffix)
	if textWidth <= 0 {
		subject = ""
	} else {
		subject = kit.TruncateText(subject, textWidth)
	}
	return kit.TruncateText(prefix+subject+suffix, max(1, width)) + "\n"
}

func itemIDWidth(items []*todo.Item) int {
	width := 0
	for _, t := range items {
		if todo.BackgroundTaskID(t) == "" {
			width = max(width, len(t.ID)+1)
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
