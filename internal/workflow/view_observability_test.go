package workflow

import (
	"fmt"
	"strings"
	"testing"
)

func TestActivityWindowPinsRunningSibling(t *testing.T) {
	events := []ActivityEvent{{Node: "A", ToolID: "running", Text: "Read important"}}
	for i := 0; i < 20; i++ {
		events = append(events, ActivityEvent{Node: "B", ToolID: fmt.Sprint(i), ToolFinished: true, Text: "Read noisy"})
	}
	view := (&Workflow{}).ActivityStreamView(events, 80)
	if !strings.Contains(view, "Read important") || !strings.Contains(view, "omitted") {
		t.Fatalf("running sibling or overflow missing: %s", view)
	}
	selected, _ := activityViewWindow(events)
	if len(selected) != ActivityWindow+1 {
		t.Fatalf("selected %d entries", len(selected))
	}
}

func TestActivityWindowSharesRecentBudget(t *testing.T) {
	events := []ActivityEvent{{Node: "A", Text: "quiet worker"}}
	for i := 0; i < 20; i++ {
		events = append(events, ActivityEvent{Node: "B", Text: "noisy worker"})
	}
	selected, _ := activityViewWindow(events)
	if len(selected) != ActivityWindow || selected[0].Node != "A" {
		t.Fatalf("unfair selection: %#v", selected)
	}
}
