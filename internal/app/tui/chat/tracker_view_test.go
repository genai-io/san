package chat

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/todo"
)

// taskIDRe matches "#<number>" task tags in rendered output.
var taskIDRe = regexp.MustCompile(`#\d+`)

func TestRenderTrackerListShowsTaskStatus(t *testing.T) {
	tracker := todo.NewStore()

	inProgress := tracker.Create("Fix auth module", "", "", map[string]any{
		"background_task_id":       "bg-1",
		"background_status_detail": "running",
	})
	_ = tracker.Update(inProgress.ID, todo.WithStatus(todo.StatusInProgress))

	failed := tracker.Create("Fix payment module", "", "", map[string]any{
		"background_task_id":       "bg-2",
		"background_status_detail": "failed",
	})
	_ = tracker.Update(failed.ID, todo.WithStatus(todo.StatusCompleted))

	completed := tracker.Create("Ship feature", "", "", nil)
	_ = tracker.Update(completed.ID, todo.WithStatus(todo.StatusCompleted))

	pending := tracker.Create("Write tests", "", "", nil)
	_ = tracker.Update(pending.ID, todo.WithStatus(todo.StatusPending))

	view, _ := RenderTrackerList(TrackerListParams{
		CompletionShown: func(string) bool { return false },
		Items:           tracker.List(),
		StreamActive:    true,
		Width:           120,
		Blockers:        tracker.OpenBlockers,
		Executing:       func(*todo.Item) bool { return true },
	})
	plain := stripANSI(view)

	for _, want := range []string{
		"Tasks",
		"(50%)",
		"●",
		"Fix auth module",
		"●",
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

	// The breathing is driven by the shared Frame tick, not the wall clock, so
	// it is deterministic: the dot stays on every frame and changes tone.
	faint, peak := renderItem(task, itemRunning, 80, 2, nil, 0, nil, ""), renderItem(task, itemRunning, 80, 2, nil, 3, nil, "")
	if plain := stripANSI(faint); !strings.HasPrefix(plain, "● ") || plain != stripANSI(peak) || faint == peak {
		t.Fatalf("executing task should keep its filled circle and breathe by color only: %q / %q", faint, peak)
	}
	waiting := renderItem(task, itemWaiting, 80, 2, nil, 0, nil, "")
	if !strings.HasPrefix(stripANSI(waiting), "○ ") || waiting != renderItem(task, itemWaiting, 80, 2, nil, 3, nil, "") {
		t.Fatalf("pending task should have a steady hollow circle: %q", waiting)
	}
	if lipgloss.Width(waiting) != lipgloss.Width(faint) {
		t.Fatalf("starting a task moved its description: %q / %q", waiting, faint)
	}
}

func TestRenderTrackerListOrdersByID(t *testing.T) {
	tracker := todo.NewStore()

	// Create tasks with mixed statuses — an in-progress task after a pending one.
	pending1 := tracker.Create("Pending A", "", "", nil)
	_ = tracker.Update(pending1.ID, todo.WithStatus(todo.StatusPending))

	inProgress := tracker.Create("Active B", "", "", nil)
	_ = tracker.Update(inProgress.ID, todo.WithStatus(todo.StatusInProgress))

	pending2 := tracker.Create("Pending C", "", "", nil)
	_ = tracker.Update(pending2.ID, todo.WithStatus(todo.StatusPending))

	view, _ := RenderTrackerList(TrackerListParams{
		CompletionShown: func(string) bool { return false },
		Items:           tracker.List(),
		StreamActive:    true,
		Width:           120,
		Executing:       func(*todo.Item) bool { return true },
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

	first := stripANSI(renderItem(task, itemStalled, 80, 2, nil, 0, nil, ""))
	for blink := range 8 {
		if frame := stripANSI(renderItem(task, itemStalled, 80, 2, nil, blink, nil, "")); frame != first {
			t.Fatalf("stalled task animated across frames:\n%q\n%q", first, frame)
		}
	}
	if !strings.Contains(first, "[stalled]") {
		t.Fatalf("stalled task should be labelled as such, got %q", first)
	}
}

// Announced workers and finished idle plans do not consume live tracker rows.
func TestRenderTrackerListVisibility(t *testing.T) {
	stalled := &todo.Item{ID: "1", Subject: "Fix auth module", Status: todo.StatusInProgress}
	done := &todo.Item{ID: "1", Subject: "Fix auth module", Status: todo.StatusCompleted}
	worker := func(detail string) *todo.Item {
		return &todo.Item{ID: "2", Subject: "Upload video", Status: todo.StatusCompleted, Metadata: map[string]any{
			"background_task_id":       "bg-1",
			"background_status_detail": detail,
		}}
	}

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
		{"worker complete and idle", []*todo.Item{worker("completed")}, false, false},
		{"worker complete while streaming", []*todo.Item{worker("completed")}, true, false},
		{"announced failed worker has no live row", []*todo.Item{worker("failed")}, false, false},
		{"announced killed worker has no live row", []*todo.Item{worker("killed")}, false, false},
		{"announced stopped worker has no live row", []*todo.Item{worker("stopped")}, false, false},
		{"announced interrupted worker has no live row", []*todo.Item{worker(todo.StatusDetailInterrupted)}, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view, _ := RenderTrackerList(TrackerListParams{
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

	rows, _ := RenderTrackerList(TrackerListParams{
		Items:        tasks,
		StreamActive: true,
		Width:        120,
		Executing:    func(*todo.Item) bool { return true },
	})
	plain := stripANSI(rows)

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
		wantHistory   string
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
			wantIDs: []string{"1", "3", "5", "9"},
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
			name:        "abnormal endings stay after completions leave",
			count:       11,
			status:      todo.StatusCompleted,
			overrides:   map[int]string{1: "failed", 2: "killed", 3: "stopped", 4: todo.StatusDetailInterrupted},
			wantHistory: "1 failed · 2 stopped · 1 interrupted",
		},
		{
			name:        "many failed agents",
			count:       11,
			status:      "failed",
			wantHistory: "11 failed",
		},
		{
			name:          "hidden failures stay in the summary",
			count:         11,
			status:        todo.StatusInProgress,
			overrides:     map[int]string{11: "failed"},
			wantIDs:       []string{"3", "4", "5", "6", "7", "8", "9", "10"},
			wantSummaries: []string{"2 more in progress"},
			wantHistory:   "1 failed",
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
			rows, history := RenderTrackerList(TrackerListParams{
				Items: items, StreamActive: true, Width: 120,
				Executing: func(*todo.Item) bool {
					checks++
					return true
				},
			})
			if history != tc.wantHistory {
				t.Fatalf("history = %q, want %q", history, tc.wantHistory)
			}
			plain := stripANSI(rows)
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
			wantLines := len(tc.wantIDs)
			if len(tc.wantIDs) > 0 {
				wantLines++ // Only current rows need a Tasks heading.
			}
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

	plain := renderItem(item, itemRunning, 80, 2, nil, 0, nil, "")
	tinted := renderItem(item, itemRunning, 80, 2, nil, 0, colors, "")

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

// Background rows describe the work directly and stay out of plan progress.
func TestRenderTrackerListBackgroundRowsDescribeWork(t *testing.T) {
	tracker := todo.NewStore()

	worker := tracker.Create("Save Jenkins script", "", "", map[string]any{
		"background_task_id":   "bg-1",
		"background_task_type": "bash",
	})
	_ = tracker.Update(worker.ID, todo.WithStatus(todo.StatusInProgress))
	render := func() string {
		rows, _ := RenderTrackerList(TrackerListParams{
			Items:     tracker.List(),
			Width:     120,
			Executing: func(*todo.Item) bool { return true },
		})
		return stripANSI(rows)
	}

	plain := render()
	if !strings.HasPrefix(plain, " ≣ Tasks\n ● Save Jenkins script") || strings.Contains(plain, "%") || strings.Count(plain, "\n") != 2 {
		t.Errorf("a worker should align with the Tasks heading without plan progress:\n%s", plain)
	}
	if strings.Contains(plain, "bash") || taskIDRe.MatchString(plain) {
		t.Errorf("worker row should lead with the work description:\n%s", plain)
	}

	plan := tracker.Create("Write tests", "", "", nil)
	_ = tracker.Update(plan.ID, todo.WithStatus(todo.StatusCompleted))
	if plain := render(); !strings.Contains(plain, " ≣ Tasks (100%)\n   Background workers\n ● Save Jenkins script") || !strings.Contains(plain, "\n   Plan\n ● #") {
		t.Errorf("progress should count plan items only; both groups should share the left edge:\n%s", plain)
	}
}

func TestRenderTrackerListAlignedColumns(t *testing.T) {
	items := []*todo.Item{
		{Subject: "Measure targeted Go coverage", Status: todo.StatusInProgress, Metadata: map[string]any{"background_task_id": "running"}},
		{Subject: "Review the results", Status: todo.StatusPending, Metadata: map[string]any{"background_task_id": "pending"}},
	}
	for _, tc := range []struct {
		width  int
		margin string
	}{
		{31, ""}, {32, " "}, {80, " "},
	} {
		rows, _ := RenderTrackerList(TrackerListParams{
			Items: items, Width: tc.width, Executing: func(*todo.Item) bool { return true },
		})
		plain := stripANSI(rows)
		want := tc.margin + "≣ Tasks\n" + tc.margin + "● Measure targeted Go coverage\n" + tc.margin + "○ Review the results\n"
		if plain != want {
			t.Fatalf("width %d: task labels or margins did not align with the title:\n%s", tc.width, plain)
		}
		if tc.width == 80 {
			t.Logf("\n%s", plain)
		}
	}
}

func TestTrackerHeadingOmitsRunningCount(t *testing.T) {
	worker := func(id, status string) *todo.Item {
		return &todo.Item{Subject: id, Status: status, Metadata: map[string]any{"background_task_id": id}}
	}
	items := []*todo.Item{
		worker("active", todo.StatusInProgress),
		worker("approval", todo.StatusInProgress),
		worker("stalled", todo.StatusInProgress),
		worker("pending", todo.StatusPending),
	}
	params := TrackerListParams{
		Items:            items,
		AwaitingApproval: map[string]bool{"approval": true},
		Executing:        func(item *todo.Item) bool { return item.Subject != "stalled" },
	}
	const heading = "≣ Tasks"
	for _, tc := range []struct {
		width int
		want  string
	}{
		{80, " " + heading},
		{lipgloss.Width(heading), heading},
		{lipgloss.Width(heading) - 1, "Tasks"},
	} {
		params.Width = tc.width
		rows, _ := RenderTrackerList(params)
		if got, _, _ := strings.Cut(stripANSI(rows), "\n"); got != tc.want {
			t.Errorf("width %d: heading = %q, want %q", tc.width, got, tc.want)
		}
	}

	params.Width = 80
	for i := range maxVisibleItems {
		params.Items = append(params.Items, worker("extra-"+strconv.Itoa(i), todo.StatusInProgress))
	}
	rows, _ := RenderTrackerList(params)
	if head, _, _ := strings.Cut(stripANSI(rows), "\n"); head != " ≣ Tasks" || !strings.Contains(stripANSI(rows), "\n … ") || !strings.Contains(rows, "more in progress") {
		t.Fatalf("folded task counts should appear only in the overflow row:\n%s", rows)
	}
}

func TestRenderTrackerListHeadingFitsNarrowTerminal(t *testing.T) {
	worker := &todo.Item{Subject: "Compress video", Status: todo.StatusPending,
		Metadata: map[string]any{"background_task_id": "bg-1"}}
	plan := &todo.Item{ID: "1", Subject: "Write tests", Status: todo.StatusCompleted}
	for _, items := range [][]*todo.Item{{worker}, {worker, plan}} {
		for _, width := range []int{1, 4, 7, 12, 31, 32, 80} {
			rows, _ := RenderTrackerList(TrackerListParams{Items: items, StreamActive: true, Width: width})
			plain := stripANSI(rows)
			for line := range strings.SplitSeq(strings.TrimSuffix(plain, "\n"), "\n") {
				if lipgloss.Width(line) > width {
					t.Fatalf("tracker exceeded %d columns:\n%s", width, plain)
				}
			}
		}
	}
}

func TestRenderTrackerListRetiresCompletedWorkerWithoutDeletingIt(t *testing.T) {
	store := todo.NewStore()
	done := store.Create("Upload finished", "", "", map[string]any{"background_task_id": "bg-done"})
	if err := store.Update(done.ID, todo.WithStatus(todo.StatusCompleted)); err != nil {
		t.Fatal(err)
	}
	store.Create("Compress video", "", "", map[string]any{"background_task_id": "bg-active"})
	for _, streaming := range []bool{false, true} {
		rows, history := RenderTrackerList(TrackerListParams{Items: store.List(), StreamActive: streaming, Width: 80})
		plain := stripANSI(rows)
		if history != "" || strings.Contains(plain, "Upload finished") || strings.Contains(plain, "completed") || !strings.Contains(plain, "Compress video") {
			t.Fatalf("successful worker should leave without a history summary:\n%s", plain)
		}
	}
	if items := store.List(); len(items) != 2 || items[0].Status != todo.StatusCompleted {
		t.Fatalf("rendering changed task records: %+v", items)
	}
}

func TestRenderTrackerRowReservesSpaceForStatus(t *testing.T) {
	for _, subject := range []string{strings.Repeat("Compress video ", 10), strings.Repeat("压缩视频", 10)} {
		item := &todo.Item{
			Subject: subject, ActiveForm: subject, Status: todo.StatusInProgress,
			StatusChangedAt: time.Now().Add(-70 * time.Second),
			Metadata:        map[string]any{"background_task_id": "bg-1", "background_status_detail": "failed"},
		}
		for _, tc := range []struct {
			phase  itemPhase
			detail string
		}{
			{itemRunning, "· 1m "},
			{itemStalled, "[stalled]"},
			{itemAborted, "[failed]"},
		} {
			for _, width := range []int{1, 12, 40, 80} {
				plain := stripANSI(renderItem(item, tc.phase, width, 0, nil, 0, nil, ""))
				if lipgloss.Width(plain) > width || strings.Count(plain, "\n") != 1 {
					t.Fatalf("row exceeded %d columns:\n%s", width, plain)
				}
				if width >= 40 && (!strings.Contains(plain, "…") || !strings.Contains(plain, tc.detail)) {
					t.Fatalf("long description hid %q:\n%s", tc.detail, plain)
				}
			}
		}
	}
}
