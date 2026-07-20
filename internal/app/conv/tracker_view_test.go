package conv

import (
	"regexp"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/todo"
)

// taskIDRe matches "#<number>" task tags in rendered output.
var taskIDRe = regexp.MustCompile(`#\d+`)

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
		Blockers:     todo.Default().OpenBlockers,
		Executing:    func(*todo.Task) bool { return true },
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
		frame := stripANSI(renderTask(task, taskRunning, 80, 2, nil, blink))
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

func TestRenderTrackerListOrdersByID(t *testing.T) {
	todo.Initialize(todo.Options{})
	t.Cleanup(func() { todo.Default().Reset() })

	// Create tasks with mixed statuses — an in-progress task after a pending one.
	pending1 := todo.Default().Create("Pending A", "", "", nil)
	_ = todo.Default().Update(pending1.ID, todo.WithStatus(todo.StatusPending))

	inProgress := todo.Default().Create("Active B", "", "", nil)
	_ = todo.Default().Update(inProgress.ID, todo.WithStatus(todo.StatusInProgress))

	pending2 := todo.Default().Create("Pending C", "", "", nil)
	_ = todo.Default().Update(pending2.ID, todo.WithStatus(todo.StatusPending))

	view := RenderTrackerList(TrackerListParams{
		Tasks:        todo.Default().List(),
		AllDone:      false,
		StreamActive: true,
		Width:        120,
		Executing:    func(*todo.Task) bool { return true },
	})
	plain := stripANSI(view)

	ids := taskIDRe.FindAllString(plain, -1)
	want := []string{"#1", "#2", "#3"}
	if !equalSlice(ids, want) {
		t.Fatalf("task order:\n  got:  %v\n  want: %v\n\nfull output:\n%s", ids, want, plain)
	}
}

// equalSlice reports whether two string slices are equal.
func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A task marked in progress whose executor is gone must render identically on
// every frame. The status field alone used to drive the pulse, so a task the
// model never closed out kept animating after the turn ended.
func TestRenderTaskHoldsStillWhenStalled(t *testing.T) {
	task := &todo.Task{ID: "1", Subject: "Fix auth module", Status: todo.StatusInProgress}

	first := stripANSI(renderTask(task, taskStalled, 80, 2, nil, 0))
	for blink := range 4 * trackerPulseTicks {
		if frame := stripANSI(renderTask(task, taskStalled, 80, 2, nil, blink)); frame != first {
			t.Fatalf("stalled task animated across frames:\n%q\n%q", first, frame)
		}
	}
	if !strings.Contains(first, "[stalled]") {
		t.Fatalf("stalled task should be labelled as such, got %q", first)
	}
}

func TestPhaseOf(t *testing.T) {
	worker := func(statusDetail string) map[string]any {
		return map[string]any{
			"background_task_id":       "bg-1",
			"background_status_detail": statusDetail,
		}
	}

	cases := []struct {
		name      string
		status    string
		metadata  map[string]any
		executing bool
		want      taskPhase
	}{
		{"pending", todo.StatusPending, nil, false, taskWaiting},
		{"in progress with executor", todo.StatusInProgress, nil, true, taskRunning},
		{"in progress without executor", todo.StatusInProgress, nil, false, taskStalled},
		{"completed", todo.StatusCompleted, nil, false, taskFinished},
		{"worker finished", todo.StatusCompleted, worker(""), false, taskFinished},
		{"worker failed", todo.StatusCompleted, worker("failed"), false, taskAborted},
		{"worker killed", todo.StatusCompleted, worker("killed"), false, taskAborted},
		{"worker stopped", todo.StatusCompleted, worker("stopped"), false, taskAborted},
		{"worker orphaned", todo.StatusCompleted, worker(todo.StatusDetailInterrupted), false, taskAborted},
		// A terminal status wins regardless of what the executor says, so a
		// stale "still running" answer cannot resurrect a finished task.
		{"completed despite executor", todo.StatusCompleted, nil, true, taskFinished},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := &todo.Task{ID: "1", Subject: "Task", Status: tc.status, Metadata: tc.metadata}
			if got := phaseOf(task, tc.executing); got != tc.want {
				t.Fatalf("phaseOf = %v, want %v", got, tc.want)
			}
		})
	}
}
