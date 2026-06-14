package conv

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/task/tracker"
)

const maxVisibleTasks = 8

// trackerPulseTicks is the number of spinner frames per ●/◌ swap of an
// in-progress task. At ~360ms per frame this gives a ~1.1s breathe — calmer
// than the agent icon's faster blink (cf. agentBlinkTicks in tool_render.go),
// suiting the tracker's quieter role.
const trackerPulseTicks = 3

// TrackerListParams holds the parameters for rendering a tracker list.
type TrackerListParams struct {
	Tasks        []*tracker.Task
	AllDone      bool
	StreamActive bool
	Width        int
	SpinnerView  string
	Blockers     func(taskID string) []string
	// Blink is the shared tick counter that drives every liveness animation,
	// so the in-progress pulse advances on the same cadence as the spinner and
	// agent icons instead of jittering off the wall clock.
	Blink int
}

// RenderTrackerList renders a compact task list above the input area.
// Returns empty string when there are no tasks or all are completed and idle.
func RenderTrackerList(params TrackerListParams) string {
	if len(params.Tasks) == 0 {
		return ""
	}

	if params.AllDone && !params.StreamActive {
		return ""
	}

	counts := countTaskStatuses(params.Tasks)
	completed := counts.done + counts.failed

	var sb strings.Builder
	headerStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim).Bold(true)
	mutedStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	pct := 0
	if len(params.Tasks) > 0 {
		pct = completed * 100 / len(params.Tasks)
	}
	sb.WriteString("  " + headerStyle.Render("Tasks") + " " + mutedStyle.Render(fmt.Sprintf("(%d%%)", pct)))
	sb.WriteString("\n")

	idWidth := taskIDWidth(params.Tasks)

	sorted := make([]*tracker.Task, 0, len(params.Tasks))
	var active []*tracker.Task
	var rest []*tracker.Task
	for _, t := range params.Tasks {
		if t.Status == tracker.StatusInProgress {
			active = append(active, t)
		} else {
			rest = append(rest, t)
		}
	}
	sorted = append(sorted, active...)
	sorted = append(sorted, rest...)

	rendered := 0
	for _, t := range sorted {
		if rendered >= maxVisibleTasks && t.Status != tracker.StatusInProgress {
			continue
		}
		sb.WriteString(renderTask(t, params.Width, idWidth, params.Blockers, params.Blink))
		rendered++
	}

	return sb.String()
}

func renderTask(t *tracker.Task, width, idWidth int, blockers func(string) []string, blink int) string {
	indent := "  "
	idTag := fmt.Sprintf("%-*s", idWidth, "#"+t.ID)
	maxTextLen := max(width-len(indent)-idWidth-8, 12)
	subject := kit.TruncateText(t.Subject, maxTextLen)
	mutedStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	statusDetail := kit.MapString(t.Metadata, "background_status_detail")

	switch t.Status {
	case tracker.StatusCompleted:
		if statusDetail == "failed" || statusDetail == "killed" {
			failedStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Error)
			return renderTaskLine(indent, failedStyle.Render("!"), idTag, subject, mutedStyle.Render("["+statusDetail+"]"))
		}
		return renderTaskLine(indent, trackerCompletedStyle.Render("●"), idTag, subject, "")

	case tracker.StatusInProgress:
		displayText := subject
		if t.ActiveForm != "" {
			displayText = kit.TruncateText(t.ActiveForm, maxTextLen)
		}
		// Pulse on the shared frame tick (now a true ~360ms clock; see Blink in
		// app.Update) rather than the wall clock, which only sampled on redraws
		// and so flickered irregularly.
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
		return renderTaskLine(indent, activeStyle.Render(activeIcon), idTag, displayText, detail)

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
		return renderTaskLine(indent, trackerPendingStyle.Render("○"), idTag, subject, detail)
	}
}

func renderTaskLine(indent, icon, id, subject, detail string) string {
	line := indent + icon + "  " + id + "  " + subject
	if detail != "" {
		line += "  " + detail
	}
	return line + "\n"
}

type taskStatusCounts struct {
	done   int
	active int
	todo   int
	failed int
}

func countTaskStatuses(tasks []*tracker.Task) taskStatusCounts {
	var counts taskStatusCounts
	for _, t := range tasks {
		statusDetail := kit.MapString(t.Metadata, "background_status_detail")
		switch {
		case t.Status == tracker.StatusCompleted && (statusDetail == "failed" || statusDetail == "killed"):
			counts.failed++
		case t.Status == tracker.StatusCompleted:
			counts.done++
		case t.Status == tracker.StatusInProgress:
			counts.active++
		default:
			counts.todo++
		}
	}
	return counts
}

func taskIDWidth(tasks []*tracker.Task) int {
	width := 2
	for _, t := range tasks {
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
