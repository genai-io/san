package conv

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/todo"
)

// taskIDRe matches "#<number>" task tags in rendered output.
var taskIDRe = regexp.MustCompile(`#\d+`)

func TestRenderTrackerListShowsTaskStatus(t *testing.T) {
	todo.Initialize()
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
		frame := stripANSI(renderItem(task, itemRunning, 80, 2, nil, blink, nil))
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
	todo.Initialize()
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

	first := stripANSI(renderItem(task, itemStalled, 80, 2, nil, 0, nil))
	for blink := range 4 * trackerPulseTicks {
		if frame := stripANSI(renderItem(task, itemStalled, 80, 2, nil, blink, nil)); frame != first {
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

// Completed rows yield space to unfinished work when the list overflows.
func TestRenderTrackerListFoldsLeadingFinished(t *testing.T) {
	tasks := make([]*todo.Item, 0, maxVisibleItems+3)
	for i := 1; i <= maxVisibleItems+3; i++ {
		id := strconv.Itoa(i)
		tasks = append(tasks, &todo.Item{
			ID:      id,
			Subject: "Task " + id,
			Status:  todo.StatusCompleted,
		})
	}
	// The tail is the work still in flight — it must never fold away.
	tasks[len(tasks)-3].Status = todo.StatusInProgress // #9
	tasks[len(tasks)-2].Status = todo.StatusPending    // #10
	tasks[len(tasks)-1].Status = todo.StatusPending    // #11

	plain := stripANSI(RenderTrackerList(TrackerListParams{
		Items:        tasks,
		StreamActive: true,
		Width:        120,
		Executing:    func(*todo.Item) bool { return true },
	}))

	ids := taskIDRe.FindAllString(plain, -1)
	want := []string{"#4", "#5", "#6", "#7", "#8", "#9", "#10", "#11"}
	if !slices.Equal(ids, want) {
		t.Fatalf("visible tasks:\n  got:  %v\n  want: %v\n\nfull output:\n%s", ids, want, plain)
	}
	if !strings.Contains(plain, "3 completed") {
		t.Fatalf("folded finished items should be summarized, not dropped silently:\n%s", plain)
	}
}

func TestRenderTrackerListLimitsBackgroundTasks(t *testing.T) {
	cases := []struct {
		name          string
		count         int
		status        string
		overrides     map[int]string
		wantIDs       []string
		wantSummaries []string
	}{
		{
			name:    "at the limit",
			count:   8,
			status:  todo.StatusInProgress,
			wantIDs: []string{"1", "2", "3", "4", "5", "6", "7", "8"},
		},
		{
			name:   "interleaved completions",
			count:  11,
			status: todo.StatusCompleted,
			overrides: map[int]string{
				1: todo.StatusInProgress, 3: todo.StatusInProgress,
				5: todo.StatusPending, 9: todo.StatusInProgress,
			},
			wantIDs:       []string{"1", "3", "5", "7", "8", "9", "10", "11"},
			wantSummaries: []string{"3 completed"},
		},
		{
			name:          "many running agents",
			count:         25,
			status:        todo.StatusInProgress,
			wantIDs:       []string{"18", "19", "20", "21", "22", "23", "24", "25"},
			wantSummaries: []string{"17 more in progress"},
		},
		{
			name:          "in progress before pending",
			count:         11,
			status:        todo.StatusPending,
			overrides:     map[int]string{1: todo.StatusInProgress},
			wantIDs:       []string{"1", "5", "6", "7", "8", "9", "10", "11"},
			wantSummaries: []string{"3 more pending"},
		},
		{
			name:          "abnormal endings before completions",
			count:         11,
			status:        todo.StatusCompleted,
			overrides:     map[int]string{1: "failed", 2: "killed", 3: "stopped", 4: todo.StatusDetailInterrupted},
			wantIDs:       []string{"1", "2", "3", "4", "8", "9", "10", "11"},
			wantSummaries: []string{"3 completed"},
		},
		{
			name:          "many failed agents",
			count:         11,
			status:        "failed",
			wantIDs:       []string{"4", "5", "6", "7", "8", "9", "10", "11"},
			wantSummaries: []string{"3 failed/stopped"},
		},
		{
			name:          "hidden failures stay in the summary",
			count:         11,
			status:        todo.StatusInProgress,
			overrides:     map[int]string{11: "failed"},
			wantIDs:       []string{"3", "4", "5", "6", "7", "8", "9", "10"},
			wantSummaries: []string{"2 more in progress", "1 failed/stopped"},
		},
	}
	rowRE := regexp.MustCompile(`\bTask (\d+)\b`)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items := make([]*todo.Item, 0, tc.count)
			for i := 1; i <= tc.count; i++ {
				id := strconv.Itoa(i)
				status := tc.status
				if override, ok := tc.overrides[i]; ok {
					status = override
				}
				metadata := map[string]any{
					"background_task_id":   "bg-" + id,
					"background_task_type": "agent",
				}
				switch status {
				case "failed", "killed", "stopped", todo.StatusDetailInterrupted:
					metadata["background_status_detail"] = status
					status = todo.StatusCompleted
				}
				items = append(items, &todo.Item{ID: id, Subject: "Task " + id, Status: status, Metadata: metadata})
			}
			checks := 0
			plain := stripANSI(RenderTrackerList(TrackerListParams{
				Items: items, StreamActive: true, Width: 120,
				Executing: func(*todo.Item) bool {
					checks++
					return true
				},
			}))
			var ids []string
			for _, match := range rowRE.FindAllStringSubmatch(plain, -1) {
				ids = append(ids, match[1])
			}
			if !slices.Equal(ids, tc.wantIDs) {
				t.Fatalf("visible tasks = %v, want %v:\n%s", ids, tc.wantIDs, plain)
			}
			for _, want := range tc.wantSummaries {
				if !strings.Contains(plain, want) {
					t.Fatalf("missing overflow summary %q:\n%s", want, plain)
				}
			}
			wantLines := 1 + len(tc.wantIDs)
			if len(tc.wantSummaries) > 0 {
				wantLines++
			}
			if lines := strings.Count(plain, "\n"); lines != wantLines {
				t.Fatalf("panel has %d lines, want %d:\n%s", lines, wantLines, plain)
			}
			if checks != len(tc.wantIDs) {
				t.Fatalf("queried %d executors for %d visible rows", checks, len(tc.wantIDs))
			}
		})
	}
}

// A row owned by a background agent is tinted with that agent's color; a plain
// todo keeps the status palette. The tint changes only color, never the text.
func TestRenderItemTintsAgentOwnedRows(t *testing.T) {
	item := &todo.Item{ID: "1", Subject: "Audit deps", Status: todo.StatusInProgress, Owner: "explorer"}
	colors := map[string]string{"explorer": "yellow"}

	plain := renderItem(item, itemRunning, 80, 2, nil, 0, nil)
	tinted := renderItem(item, itemRunning, 80, 2, nil, 0, colors)

	if stripANSI(plain) != stripANSI(tinted) {
		t.Fatalf("agent tint changed the row text, not just its color:\n  %q\n  %q", stripANSI(plain), stripANSI(tinted))
	}
	if plain == tinted {
		t.Fatalf("agent-owned row should be tinted with the agent color, got identical output:\n%q", tinted)
	}
}

func TestAgentForeground(t *testing.T) {
	colors := map[string]string{"explorer": "yellow"}

	if _, ok := agentForeground(&todo.Item{Owner: "explorer"}, colors); !ok {
		t.Fatal("agent-owned item should resolve its color")
	}
	if _, ok := agentForeground(&todo.Item{Owner: "Explorer"}, colors); !ok {
		t.Fatal("owner lookup should be case-insensitive")
	}
	if _, ok := agentForeground(&todo.Item{Owner: ""}, colors); ok {
		t.Fatal("a plain todo (no owner) must not resolve a color")
	}
	if _, ok := agentForeground(&todo.Item{Owner: "ghost"}, colors); ok {
		t.Fatal("an owner with no configured color must not resolve one")
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

// Background rows lead with their kind, not a tracker ID the user cannot act
// on, and stay out of the plan's progress; with no plan the header says so.
func TestRenderTrackerListLabelsBackgroundTasks(t *testing.T) {
	todo.Initialize()
	t.Cleanup(func() { todo.Default().Reset() })

	worker := todo.Default().Create("Save Jenkins script", "", "", map[string]any{
		"background_task_id":   "bg-1",
		"background_task_type": "bash",
	})
	_ = todo.Default().Update(worker.ID, todo.WithStatus(todo.StatusInProgress))
	render := func() string {
		return stripANSI(RenderTrackerList(TrackerListParams{
			Items:     todo.Default().List(),
			Width:     120,
			Executing: func(*todo.Item) bool { return true },
		}))
	}

	plain := render()
	if !strings.Contains(plain, "Background") || strings.Contains(plain, "%") {
		t.Errorf("worker-only header should read Background without progress:\n%s", plain)
	}
	if !strings.Contains(plain, "bash  Save Jenkins script") || taskIDRe.MatchString(plain) {
		t.Errorf("worker row should lead with its kind, not an ID:\n%s", plain)
	}

	plan := todo.Default().Create("Write tests", "", "", nil)
	_ = todo.Default().Update(plan.ID, todo.WithStatus(todo.StatusCompleted))
	if plain := render(); !strings.Contains(plain, "Tasks (100%)") {
		t.Errorf("progress should count plan items only:\n%s", plain)
	}
}
