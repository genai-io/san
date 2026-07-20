package conv

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/task"
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
		Items:        todo.Default().List(),
		StreamActive: true,
		Width:        120,
		Blockers:     todo.Default().OpenBlockers,
		Executing:    func(*todo.Item) bool { return true },
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
	task := &todo.Item{ID: "1", Subject: "Fix auth module", Status: todo.StatusInProgress}

	// The pulse is driven by the shared Blink tick, not the wall clock, so a
	// full cadence is deterministic: advancing Blink across one period must show
	// both the solid (●) and dim (◌) phases.
	var hasSolid, hasDim bool
	for blink := range 4 * trackerPulseTicks {
		frame := stripANSI(renderItem(task, itemRunning, 80, 2, nil, blink))
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
		Items:        todo.Default().List(),
		StreamActive: true,
		Width:        120,
		Executing:    func(*todo.Item) bool { return true },
	})
	plain := stripANSI(view)

	ids := taskIDRe.FindAllString(plain, -1)
	want := []string{"#1", "#2", "#3"}
	if !slices.Equal(ids, want) {
		t.Fatalf("task order:\n  got:  %v\n  want: %v\n\nfull output:\n%s", ids, want, plain)
	}
}

// A task marked in progress whose executor is gone must render identically on
// every frame. The status field alone used to drive the pulse, so a task the
// model never closed out kept animating after the turn ended.
func TestRenderTaskHoldsStillWhenStalled(t *testing.T) {
	task := &todo.Item{ID: "1", Subject: "Fix auth module", Status: todo.StatusInProgress}

	first := stripANSI(renderItem(task, itemStalled, 80, 2, nil, 0))
	for blink := range 4 * trackerPulseTicks {
		if frame := stripANSI(renderItem(task, itemStalled, 80, 2, nil, blink)); frame != first {
			t.Fatalf("stalled task animated across frames:\n%q\n%q", first, frame)
		}
	}
	if !strings.Contains(first, "[stalled]") {
		t.Fatalf("stalled task should be labelled as such, got %q", first)
	}
}

// The panel's visibility gate. It hides only once every item is marked
// completed, so a stalled item — in progress with nothing executing it — is
// unfinished work and must keep the list on screen. Gating on executors instead
// would hide exactly the row #342 added to surface.
func TestRenderTrackerListVisibility(t *testing.T) {
	stalled := &todo.Item{ID: "1", Subject: "Fix auth module", Status: todo.StatusInProgress}
	done := &todo.Item{ID: "1", Subject: "Fix auth module", Status: todo.StatusCompleted}

	cases := []struct {
		name         string
		tasks        []*todo.Item
		streamActive bool
		wantVisible  bool
	}{
		{"no tasks", nil, true, false},
		{"complete and idle", []*todo.Item{done}, false, false},
		{"complete but still streaming", []*todo.Item{done}, true, true},
		{"stalled item keeps the list up", []*todo.Item{stalled}, false, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view := RenderTrackerList(TrackerListParams{
				Items:        tc.tasks,
				StreamActive: tc.streamActive,
				Width:        120,
				Executing:    func(*todo.Item) bool { return false },
			})
			if visible := view != ""; visible != tc.wantVisible {
				t.Fatalf("visible = %v, want %v; rendered:\n%s", visible, tc.wantVisible, stripANSI(view))
			}
		})
	}
}

// The list outlives the turn that filled it, so windowing from the front would
// pin the panel to the turn that stalled and hide everything since.
func TestRenderTrackerListWindowsOnNewestTasks(t *testing.T) {
	tasks := make([]*todo.Item, 0, maxVisibleItems+3)
	for i := 1; i <= maxVisibleItems+3; i++ {
		id := strconv.Itoa(i)
		tasks = append(tasks, &todo.Item{
			ID:      id,
			Subject: "Task " + id,
			Status:  todo.StatusCompleted,
		})
	}
	// The oldest task is the one left open, so it is what keeps the list alive.
	tasks[0].Status = todo.StatusInProgress

	plain := stripANSI(RenderTrackerList(TrackerListParams{
		Items:        tasks,
		StreamActive: true,
		Width:        120,
		Executing:    func(*todo.Item) bool { return false },
	}))

	ids := taskIDRe.FindAllString(plain, -1)
	want := []string{"#4", "#5", "#6", "#7", "#8", "#9", "#10", "#11"}
	if !slices.Equal(ids, want) {
		t.Fatalf("visible tasks:\n  got:  %v\n  want: %v\n\nfull output:\n%s", ids, want, plain)
	}
	if !strings.Contains(plain, "+3 more above") {
		t.Fatalf("hidden tasks should be counted, not dropped silently:\n%s", plain)
	}
}

func TestPhaseOf(t *testing.T) {
	// Mirrors setBackgroundStatusDetail: the detail is a task.TaskStatus that
	// has to land in the map as a plain string, since it is read back with a
	// .(string) assertion.
	worker := func(statusDetail task.TaskStatus) map[string]any {
		return map[string]any{
			"background_task_id":       "bg-1",
			"background_status_detail": string(statusDetail),
		}
	}

	cases := []struct {
		name      string
		status    todo.Status
		metadata  map[string]any
		executing bool
		want      itemPhase
	}{
		{"pending", todo.StatusPending, nil, false, itemWaiting},
		{"in progress with executor", todo.StatusInProgress, nil, true, itemRunning},
		{"in progress without executor", todo.StatusInProgress, nil, false, itemStalled},
		{"completed", todo.StatusCompleted, nil, false, itemFinished},
		{"worker finished", todo.StatusCompleted, worker(""), false, itemFinished},
		{"worker failed", todo.StatusCompleted, worker("failed"), false, itemAborted},
		{"worker killed", todo.StatusCompleted, worker("killed"), false, itemAborted},
		{"worker stopped", todo.StatusCompleted, worker("stopped"), false, itemAborted},
		{"worker orphaned", todo.StatusCompleted, worker(todo.StatusDetailInterrupted), false, itemAborted},
		// A terminal status wins regardless of what the executor says, so a
		// stale "still running" answer cannot resurrect a finished task.
		{"completed despite executor", todo.StatusCompleted, nil, true, itemFinished},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := &todo.Item{ID: "1", Subject: "Task", Status: tc.status, Metadata: tc.metadata}
			if got := phaseOf(task, tc.executing); got != tc.want {
				t.Fatalf("phaseOf = %v, want %v", got, tc.want)
			}
		})
	}
}
