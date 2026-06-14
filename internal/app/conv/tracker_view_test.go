package conv

import (
	"strings"
	"testing"

	"github.com/genai-io/san/internal/todo"
)

func TestRenderTrackerListShowsTaskStatus(t *testing.T) {
	todo.Initialize(todo.Options{})
	t.Cleanup(func() { todo.Default().Reset() })

	inProgress := todo.Default().Create("Fix auth module", "", "", map[string]any{
		"background_task_id":       "bg-1",
		"background_status_detail": "running",
	})
	_ = todo.Default().Update(inProgress.ID, todo.WithStatus(todo.StatusInProgress))

	failed := todo.Default().Create("Fix payment module", "", "", map[string]any{
		"background_task_id":       "bg-2",
		"background_status_detail": "failed",
	})
	_ = todo.Default().Update(failed.ID, todo.WithStatus(todo.StatusCompleted))

	completed := todo.Default().Create("Ship feature", "", "", nil)
	_ = todo.Default().Update(completed.ID, todo.WithStatus(todo.StatusCompleted))

	pending := todo.Default().Create("Write tests", "", "", nil)
	_ = todo.Default().Update(pending.ID, todo.WithStatus(todo.StatusPending))

	view := RenderTrackerList(TrackerListParams{
		Tasks:        todo.Default().List(),
		AllDone:      false,
		StreamActive: true,
		Width:        120,
		SpinnerView:  "•",
		Blockers:     todo.Default().OpenBlockers,
	})
	plain := stripANSI(view)

	for _, want := range []string{
		"Tasks",
		"(50%)",
		"●",
		"Fix auth module",
		"!",
		"Fix payment module",
		"[failed]",
		"●",
		"Ship feature",
		"○",
		"Write tests",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("rendered tracker list missing %q:\n%s", want, plain)
		}
	}
}

func TestRenderTaskAnimatesInProgressItem(t *testing.T) {
	task := &todo.Task{ID: "1", Subject: "Fix auth module", Status: todo.StatusInProgress}

	// The pulse is driven by the shared Blink tick, not the wall clock, so a
	// full cadence is deterministic: advancing Blink across one period must show
	// both the solid (●) and dim (◌) phases.
	var hasSolid, hasDim bool
	for blink := range 4 * trackerPulseTicks {
		frame := stripANSI(renderTask(task, 80, 2, nil, blink))
		if strings.Contains(frame, "●") {
			hasSolid = true
		}
		if strings.Contains(frame, "◌") {
			hasDim = true
		}
	}

	if !hasSolid {
		t.Fatal("in-progress task should show solid active icon (●) at some point")
	}
	if !hasDim {
		t.Fatal("in-progress task should show dim active icon (◌) at some point")
	}
}
