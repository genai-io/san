package chat

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/tui/widgets"
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
	// CompletionShown keeps a terminal worker visible until its notice is shown.
	// Nil treats completions as already announced.
	CompletionShown func(taskID string) bool
	// Executing reports whether the executor behind an item is running right
	// now. An in_progress item only animates when Executing says so: the
	// status field records intent and outlives the process that wrote it, so
	// it cannot by itself justify a moving pixel.
	Executing func(t *todo.Item) bool
	// Frame is the shared frame-tick counter (see FrameClock.Frame) that
	// breathes the in-progress item's live dot.
	Frame int
	// AgentColors maps an agent's name to its display color. An item owned by a
	// background agent (Owner set) is tinted with that color, matching the
	// agent's launch line in the flow. Plain user todos have no owner and keep
	// the status palette.
	AgentColors map[string]string
	// WorkerStatus provides display-only live activity and output-log health.
	AwaitingApproval map[string]bool
	WorkerStatus     func(taskID string) task.TaskInfo
}

// RenderTrackerList returns the current task rows and a plain-text summary of
// announced background task failures, stops, and interruptions.
func RenderTrackerList(params TrackerListParams) (rows, history string) {
	if len(params.Items) == 0 {
		return "", ""
	}

	items := make([]*todo.Item, 0, len(params.Items))
	var failed, stopped, interrupted int
	for _, t := range params.Items {
		id := todo.BackgroundTaskID(t)
		if id != "" && t.Status == todo.StatusCompleted && (params.CompletionShown == nil || params.CompletionShown(id)) {
			switch todo.BackgroundStatusDetail(t) {
			case string(task.StatusFailed):
				failed++
			case string(task.StatusStopped), string(task.StatusKilled):
				stopped++
			case todo.StatusDetailInterrupted:
				interrupted++
			}
			continue
		}
		items = append(items, t)
	}
	history = summarizeTrackerHistory(failed, stopped, interrupted)
	plansDone, plans := PlanProgress(params.Items)
	if len(items) == 0 || (!params.StreamActive && plansDone == len(items)) {
		return "", history
	}

	width := max(1, params.Width)
	margin := ""
	if width >= 32 {
		margin = " " // Match the queued prompt gutter without changing task alignment.
		width--
	}
	var sb strings.Builder
	headerStyle := lipgloss.NewStyle().Foreground(widgets.CurrentTheme.TextDim).Bold(true)
	mutedStyle := lipgloss.NewStyle().Foreground(widgets.CurrentTheme.Muted)
	header := headerStyle.Render("Tasks")
	groupIndent := ""
	if lipgloss.Width("≣ Tasks") <= width {
		header = mutedStyle.Render("≣") + " " + header
		groupIndent = "  " // Group labels use the same text column as task rows.
	}
	visible, hidden := visibleTrackerItems(items)
	idWidth := itemIDWidth(visible)

	// Query executor liveness only for visible rows.
	lastPlan := false
	for i, t := range visible {
		id := todo.BackgroundTaskID(t)
		isPlan := id == ""
		if plans > 0 && plans < len(items) && (i == 0 || isPlan != lastPlan) {
			label := "Background workers"
			if isPlan {
				label = "Plan"
			}
			sb.WriteString(margin + groupIndent + widgets.TruncateText(headerStyle.Render(label), width-len(groupIndent)) + "\n")
		}
		lastPlan = isPlan
		phase := phaseOf(t, params.Executing != nil && params.Executing(t))
		haveWorkerStatus := !isPlan && params.WorkerStatus != nil
		var info task.TaskInfo
		if haveWorkerStatus {
			info = params.WorkerStatus(id)
		}
		if t.Status == todo.StatusInProgress && params.AwaitingApproval[id] && !isPlan && (!haveWorkerStatus || info.Status == task.StatusRunning) {
			phase = itemApproval
		}
		progress := ""
		if phase == itemAborted {
			progress = todo.BackgroundError(t)
		}
		if haveWorkerStatus && phase != itemApproval {
			if phase == itemAborted {
				// A final reason outranks stale activity (e.g. the last Read call).
				if info.Error != "" {
					progress = info.Error
				}
				if info.Status != task.StatusFailed && widgets.IsRoutineStopReason(progress) {
					progress = ""
				}
			} else if phase == itemRunning {
				progress = info.Progress
				if progress == "" {
					// Structured callers may supply only LiveView.
					progress, _, _ = strings.Cut(info.LiveView, "\n")
				}
			}
			if info.OutputError != "" {
				if phase == itemAborted && progress != "" {
					progress += " · output log unavailable"
				} else if progress != "" {
					progress = "output log unavailable · " + progress
				} else {
					progress = "output log unavailable"
				}
			}
		}
		sb.WriteString(margin)
		sb.WriteString(renderItem(t, phase, width, idWidth, params.Blockers, params.Frame, params.AgentColors, strings.Join(strings.Fields(progress), " ")))
	}

	if folded := renderFoldedLine(hidden); folded != "" {
		sb.WriteString(margin + widgets.TruncateText(strings.TrimSuffix(folded, "\n"), width) + "\n")
	}
	// Progress is the plan's: a background task finishing is not a step of it.
	if plans > 0 {
		progress := fmt.Sprintf(" (%d%%)", plansDone*100/plans)
		if lipgloss.Width(header+progress) <= width {
			header += mutedStyle.Render(progress)
		}
	}
	return margin + widgets.TruncateText(header, width) + "\n" + sb.String(), history
}

func summarizeTrackerHistory(failed, stopped, interrupted int) string {
	var parts []string
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", failed))
	}
	if stopped > 0 {
		parts = append(parts, fmt.Sprintf("%d stopped", stopped))
	}
	if interrupted > 0 {
		parts = append(parts, fmt.Sprintf("%d interrupted", interrupted))
	}
	return strings.Join(parts, " · ")
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
			// The combined group also contains deliberate stops; only an
			// individual failure row, not this mixed count, warrants error red.
			text := fmt.Sprintf("%d %s", n, group.label)
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	mutedStyle := lipgloss.NewStyle().Foreground(widgets.CurrentTheme.Muted)
	return mutedStyle.Render("… "+strings.Join(parts, ", ")) + "\n"
}

// itemPhase is how an item reads to the user. It collapses the recorded status,
// how a finished item ended, and whether an executor is actually running it
// into one exhaustive set, so each render branch answers to exactly one phase.
type itemPhase int

const itemApproval itemPhase = -1

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

func renderItem(t *todo.Item, phase itemPhase, width, idWidth int, blockers func(string) []string, frame int, agentColors map[string]string, progress string) string {
	mutedStyle := lipgloss.NewStyle().Foreground(widgets.CurrentTheme.Muted)
	gutter := " "
	if todo.BackgroundTaskID(t) == "" {
		gutter += fmt.Sprintf("%-*s ", idWidth, "#"+t.ID)
	}
	subject := t.Subject
	row := func(icon, subject, detail string) string {
		if progress != "" && width/2 > 3 {
			detail += mutedStyle.Render(" · " + widgets.TruncateText(progress, width/2-3))
		}
		return renderItemLine(icon+gutter, subject, detail, width)
	}

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
	case itemApproval:
		return renderItemLine(tint(mutedStyle).Render("○")+gutter, tint(lipgloss.NewStyle()).Render(activeText(t)), mutedStyle.Render(" · Awaiting approval"), width)
	case itemAborted:
		abortedStyle := lipgloss.NewStyle().Foreground(widgets.CurrentTheme.Error)
		if detail := todo.BackgroundStatusDetail(t); detail == "stopped" || detail == "killed" || detail == todo.StatusDetailInterrupted {
			abortedStyle = mutedStyle
		}
		detail := mutedStyle.Render(" [" + todo.BackgroundStatusDetail(t) + "]")
		return row(abortedStyle.Render("●"), subject, detail)

	case itemFinished:
		detail := ""
		if todo.BackgroundTaskID(t) != "" {
			detail = mutedStyle.Render(" [completed]")
		}
		return row(tint(trackerCompletedStyle).Render("●"), tint(lipgloss.NewStyle()).Render(subject), detail)

	case itemStalled:
		// Nothing is executing this item, so draw it at rest. Reaching here
		// means the status outlived its executor within a live session — the
		// model marked an item in_progress and moved on without closing it.
		// Animating would claim work that isn't happening.
		return row(mutedStyle.Render("○"), activeText(t), mutedStyle.Render(" [stalled]"))

	case itemRunning:
		// Breathe on the shared frame tick (a true ~360ms clock; see FrameClock)
		// rather than the wall clock, which only sampled on redraws and so
		// flickered irregularly.
		prefix := widgets.LiveTaskDot(frame) + gutter
		subject := tint(lipgloss.NewStyle()).Render(activeText(t))
		detail := ""
		if elapsed := formatElapsedTime(t.StatusChangedAt); elapsed != "" {
			detail = mutedStyle.Render(" · " + elapsed)
		}
		// Spend spare columns on activity, not the task's identity. When the
		// clock grows, trim or drop activity without shifting its starting column.
		room := width - lipgloss.Width(prefix+subject+detail) - lipgloss.Width(" · ")
		if progress != "" && room >= min(8, lipgloss.Width(progress)) {
			detail = mutedStyle.Render(" · "+widgets.TruncateText(progress, room)) + detail
		}
		return renderItemLine(prefix, subject, detail, width)

	default:
		detail := ""
		if blockers != nil {
			if bl := blockers(t.ID); len(bl) > 0 {
				blockerRefs := make([]string, len(bl))
				for i, b := range bl {
					blockerRefs[i] = "#" + b
				}
				blockedStyle := lipgloss.NewStyle().Foreground(widgets.CurrentTheme.Error)
				detail = blockedStyle.Render(" ← " + strings.Join(blockerRefs, ", "))
			}
		}
		return row(tint(trackerPendingStyle).Render("○"), tint(lipgloss.NewStyle()).Render(subject), detail)
	}
}

// agentForeground returns the owning agent's display color for an item, and
// whether the item is owned by a colored agent at all — a plain user todo has no
// owner, and an owner with no configured color resolves to nothing.
func agentForeground(t *todo.Item, agentColors map[string]string) (widgets.AdaptiveColor, bool) {
	if t.Owner == "" || len(agentColors) == 0 {
		return widgets.AdaptiveColor{}, false
	}
	color, ok := agentColors[strings.ToLower(t.Owner)]
	if !ok || color == "" {
		return widgets.AdaptiveColor{}, false
	}
	return agentColor(color), true
}

func renderItemLine(prefix, subject, suffix string, width int) string {
	textWidth := width - lipgloss.Width(prefix+suffix)
	if textWidth <= 0 {
		subject = ""
	} else {
		subject = widgets.TruncateText(subject, textWidth)
	}
	return widgets.TruncateText(prefix+subject+suffix, max(1, width)) + "\n"
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
