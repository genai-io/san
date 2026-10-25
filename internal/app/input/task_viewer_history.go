package input

import (
	"fmt"
	"slices"

	"github.com/genai-io/san/internal/task"
)

func taskViewTerminal(item TaskViewItem) bool {
	switch item.Status {
	case task.StatusCompleted, task.StatusFailed, task.StatusStopped, task.StatusKilled, "interrupted":
		return true
	default:
		// Unknown states must not silently disappear into history.
		return false
	}
}

// Snapshots arrive oldest first. Keep active identities stable and put the most
// recently launched historical tasks first, without changing backend records.
func (v *TaskViewer) groupedItems() (active, history []TaskViewItem) {
	for _, item := range v.items {
		if taskViewTerminal(item) {
			history = append(history, item)
		} else {
			active = append(active, item)
		}
	}
	slices.Reverse(history)
	return active, history
}

func (v *TaskViewer) visibleItems() []TaskViewItem {
	active, history := v.groupedItems()
	if v.historyExpanded {
		return append(active, history...)
	}
	return active
}

func (v *TaskViewer) selectVisible() {
	items := v.visibleItems()
	for _, item := range items {
		if item.ID == v.selected {
			return
		}
	}
	if _, exists := v.live(v.selected); !exists && v.selected != "" {
		// A removed worker's last snapshot remains inspectable, never actionable.
		return
	}
	v.selected = ""
	if len(items) > 0 {
		v.selected = items[0].ID
	}
}

func (v *TaskViewer) historySummary(history []TaskViewItem) string {
	failed := 0
	for _, item := range history {
		if item.Status == task.StatusFailed {
			failed++
		}
	}
	line := fmt.Sprintf("History · %d", len(history))
	if failed > 0 {
		line += fmt.Sprintf(" · %d failed", failed)
	}
	if !v.historyExpanded {
		line += " (h to show)"
	}
	return line
}
