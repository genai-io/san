package input

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/flow"
)

func TestFlowReviewExecutorSelection(t *testing.T) {
	for _, tc := range []struct{ config, want string }{
		{"", "Executor subagent (default)"},
		{"executor: subagent\n", "Executor subagent (default)"},
		{"executor: main\n", "Executor main · current conversation"},
	} {
		var v FlowView
		source := "```mermaid\nflowchart LR\n summary\n```\n\n## summary\n" + tc.config + "\nReturn a report.\n"
		v.OpenDraft(flowUIFixture(), "executor", source, source, 100, 40)
		got := ansi.Strip(v.renderReview())
		if !strings.Contains(got, tc.want) {
			t.Fatalf("execution target not disclosed: %s", got)
		}
		if strings.Contains(tc.config, "main") && !strings.Contains(got, "current main agent · inherited settings") {
			t.Fatal("main node did not disclose inherited settings")
		}
		if limits := ansi.Strip(v.renderLimits()); !strings.Contains(limits, "main steps use the current conversation") {
			t.Fatalf("execution limits misstate conversation ownership: %s", limits)
		}
	}
}

func TestFlowGeneratedDraftNeedsManualSaveAndRun(t *testing.T) {
	s := flowUIFixture()
	var v FlowView
	request := v.OpenGeneration(s, "Review the parser", 100, 40)().(FlowDraftRequestMsg)
	if !v.DraftReady(request.Generation, flowUISource, nil) || v.page != "review" || v.saved || v.source != flowUISource {
		t.Fatal("generated source did not open an unsaved review")
	}
	if v.DraftReady(request.Generation, "stale source", nil) {
		t.Fatal("completed generation accepted another result")
	}
	v.HandleKeypress(flowKey("r"))
	if s.saves != 0 || s.launches != 0 || v.page != "review" {
		t.Fatal("generated source became runnable without an explicit save")
	}
	v.HandleKeypress(flowKey("e"))
	v.editor.SetValue(strings.Replace(flowUISource, "name: sample", "name: parser-review", 1))
	v.HandleKeypress(flowKey("esc"))
	if v.name != "parser-review" || v.saved {
		t.Fatal("unsaved draft name cannot be edited before first save")
	}
}

func TestFlowGenerationFailureRetryAndClose(t *testing.T) {
	s := flowUIFixture()
	var v FlowView
	request := v.OpenGeneration(s, "Review", 100, 40)().(FlowDraftRequestMsg)
	if !v.DraftReady(request.Generation, "", errors.New("provider unavailable")) || v.IsGenerating(request.Generation) || !strings.Contains(v.Render(), "provider unavailable") {
		t.Fatal("generation failure remained live or was hidden")
	}
	retry := v.HandleKeypress(flowKey("enter"))().(FlowDraftRequestMsg)
	if retry.Generation == request.Generation || retry.Goal != request.Goal || !v.IsGenerating(retry.Generation) {
		t.Fatal("retry did not create a fresh authoring generation")
	}
	v.HandleKeypress(flowKey("esc"))
	if v.DraftReady(retry.Generation, flowUISource, nil) || v.IsActive() || s.saves != 0 || s.launches != 0 {
		t.Fatal("closed authoring request accepted a late result")
	}
	request = v.OpenGeneration(s, "Review", 100, 40)().(FlowDraftRequestMsg)
	if !v.DraftReady(request.Generation, "invalid raw source", nil) || v.page != "review" || v.source != "invalid raw source" || v.plan != nil {
		t.Fatal("invalid generated source was not preserved for manual repair")
	}
	v.HandleKeypress(flowKey("s"))
	if v.confirm != "" || s.saves != 0 {
		t.Fatal("invalid generation was saved")
	}
}

func TestFlowNewStartsWithOneStep(t *testing.T) {
	c := flowController(t, nil)
	c.env.Input, c.env.Width, c.env.Height = &Model{}, 96, 32
	if _, _, err := c.handleFlowCommand(context.Background(), "new example"); err != nil {
		t.Fatal(err)
	}
	v := &c.env.Input.Flow
	if v.plan == nil || len(v.plan.Nodes) != 1 || len(v.plan.InputNames()) != 1 || v.plan.InputNames()[0] != "input" {
		t.Fatal("starter should have one step and a simple input")
	}
	initial := ansi.Strip(v.Render())
	for _, want := range []string{"○ work", "Step 1/1 · work", "Agent   default · explore", "{{input}} ← what you enter before running", "{{work}} = this step's final answer", "Final flow result"} {
		if !strings.Contains(initial, want) {
			t.Fatalf("missing %q in review:\n%s", want, initial)
		}
	}
	for _, unwanted := range []string{"Run inputs", "you provide this before running", "Draft — save a valid", "Save project flow", "Project copy:", "nodes · up to", "max_parallel", "```mermaid", "{{input.task}}", "review node"} {
		if strings.Contains(initial, unwanted) {
			t.Fatalf("review exposes %q:\n%s", unwanted, initial)
		}
	}
	v.HandleKeypress(flowKey("d"))
	limits := ansi.Strip(v.Render())
	if v.page != "details" || !strings.Contains(limits, "At most 1 step execution") || !strings.Contains(limits, "At most 1 step running") {
		t.Fatal("execution limits are missing or starter runs unnecessary steps")
	}
	v.HandleKeypress(flowKey("esc"))
	if v.page != "review" || !v.active || v.confirm != "" {
		t.Fatal("leaving details closed the unsaved draft")
	}
	v.HandleKeypress(flowKey("e"))
	edit := ansi.Strip(v.Render())
	if !strings.Contains(edit, "{{input}} = values you enter before running") || !strings.Contains(edit, "step answers, available downstream") {
		t.Fatal("source editor does not explain template values")
	}
}

func TestFlowReviewGraphAndSelectedStep(t *testing.T) {
	source := strings.Replace(flowUISource, "Use {{input.topic}}", "Use {{input.topic}}\n"+strings.Repeat("First step instructions\n", 40), 1)
	var v FlowView
	v.OpenDraft(flowUIFixture(), "sample", source, source, 100, 40)
	initial := ansi.Strip(v.Render())
	for _, want := range []string{"○ a", "○ b", "○ c", "○ d", "▼", "Step 1/4 · a", "{{input.topic}} ← topic you enter", "Read by b, c"} {
		if !strings.Contains(initial, want) {
			t.Fatalf("missing %q in review:\n%s", want, initial)
		}
	}
	if strings.Index(initial, "▼") > strings.Index(initial, "Step 1/4") || strings.Contains(initial, "Run inputs") || strings.Contains(initial, "Use {{b}} and {{c}}") {
		t.Fatal("review should show the graph first and only the selected step's instructions")
	}
	v.body.GotoBottom()
	if !strings.Contains(ansi.Strip(v.Render()), "○ a") {
		t.Fatal("scrolling instructions hid the graph")
	}
	v.HandleKeypress(flowKey("down"))
	next := ansi.Strip(v.Render())
	if v.nodeID() != "b" || v.body.YOffset() != 0 || !strings.Contains(next, "Step 2/4 · b") || !strings.Contains(next, "{{a}} ← final answer from step a") || strings.Contains(next, "First step instructions") {
		t.Fatal("selection did not replace the detail with the next step")
	}
	v.HandleKeypress(flowKey("up"))
	if v.nodeID() != "a" {
		t.Fatal("cannot return to previous step")
	}
}

func TestFlowSimpleInputCollection(t *testing.T) {
	s := flowUIFixture()
	s.source = flowDraft("sample", "work", "## work\nUse {{input}} and {{input.input}}\n")
	var v FlowView
	v.OpenDraft(s, "sample", s.source, s.source, 96, 32)
	v.HandleKeypress(flowKey("r"))
	if v.page != "inputs" || len(v.keys) != 1 || v.keys[0] != "input" || !strings.Contains(ansi.Strip(v.Render()), "Request") {
		t.Fatal("simple input did not open one explained field")
	}
	v.HandlePaste("Check storage")
	v.HandleKeypress(flowKey("enter"))
	if s.launches != 0 || v.field != len(v.keys) {
		t.Fatal("last field must await summary confirmation")
	}
	cmd := v.HandleKeypress(flowKey("enter"))
	if s.launches != 1 || s.values["input"] != "Check storage" || v.IsActive() || cmd == nil {
		t.Fatal("simple input did not launch nonblocking with a task-area clock signal")
	}
	if _, ok := cmd().(FlowStartedMsg); !ok {
		t.Fatal("launch did not start task-area animation")
	}
}

func TestFlowReviewExplainsWorkerDataFlow(t *testing.T) {
	source := flowDraft("sample", "plan --> work --> result", "## plan\nReturn a JSON object with a tasks array.\n\n## work\nfor_each: plan.tasks\nmax_workers: 2\n\nDo {{item.task}}\n\n## result\nCombine {{work}}\n")
	var v FlowView
	v.OpenDraft(flowUIFixture(), "sample", source, "", 96, 32)
	if !strings.Contains(v.renderReview(), "Read by work") {
		t.Fatal("plan does not identify the step reading its array")
	}
	v.HandleKeypress(flowKey("down"))
	for _, want := range []string{"Items   JSON array from plan.tasks", "{{item.task}} ← value from the current plan item", "combined answers from completed workers", "Read by result"} {
		if !strings.Contains(v.renderReview(), want) {
			t.Fatalf("worker review does not explain %q", want)
		}
	}
}

func TestFlowReviewKeepsPrimaryActionsVisible(t *testing.T) {
	for _, saved := range []bool{false, true} {
		var v FlowView
		baseline := ""
		if saved {
			baseline = flowUISource
		}
		v.OpenDraft(flowUIFixture(), "sample", flowUISource, baseline, 40, 24)
		out := ansi.Strip(v.Render())
		for _, action := range []string{"e edit", "s save", "Esc close"} {
			if !strings.Contains(out, action) {
				t.Fatalf("saved=%v: narrow review hides %q:\n%s", saved, action, out)
			}
		}
		if saved && !strings.Contains(out, "r run") {
			t.Fatal("saved review hides run action")
		}
		for _, line := range strings.Split(out, "\n") {
			if ansi.StringWidth(line) > 40 {
				t.Fatalf("review exceeds terminal width: %q", line)
			}
		}
	}
}

func TestFlowReviewSelectionAfterRemovingSteps(t *testing.T) {
	for _, action := range []string{"esc", "ctrl+s"} {
		t.Run(action, func(t *testing.T) {
			var v FlowView
			v.OpenDraft(flowUIFixture(), "sample", flowUISource, flowUISource, 96, 32)
			v.selected = 3
			v.HandleKeypress(flowKey("e"))
			v.editor.SetValue(flowDraft("sample", "a", "## a\nUse {{input.topic}}\n"))
			v.HandleKeypress(flowKey(action))
			if action == "ctrl+s" {
				v.HandleKeypress(flowKey("y"))
			}
			if v.page != "review" || v.nodeID() != "a" {
				t.Fatalf("edited review lost selection: page=%s node=%q", v.page, v.nodeID())
			}
			for _, want := range []string{"{{input.topic}}", "Output", "Instructions"} {
				if !strings.Contains(ansi.Strip(v.Render()), want) {
					t.Fatalf("edited review lost %s", want)
				}
			}
		})
	}
}

func TestFlowResumeModesAndWorkerPreview(t *testing.T) {
	s := flowUIFixture()
	s.r.Status = flow.RunFailed
	c := s.r.Nodes["b"]
	c.Status = flow.StatusFailed
	c.Workers = []flow.WorkerCheckpoint{{State: flow.Checkpoint{Outcome: flow.Outcome{Status: flow.StatusSucceeded}}}}
	s.r.Nodes["b"] = c
	var v FlowView
	v.OpenRun(s, "run", 110, 40)
	if v.nodeID() != "b" {
		t.Fatal("failed run should focus its failed node")
	}
	v.selected, v.follow = 3, false
	v.HandleKeypress(flowKey("r"))
	if v.nodeID() != "d" || v.resumeFrom != "" {
		t.Fatal("default resume targeted selection")
	}
	preview := v.resumePreview()
	if !strings.Contains(preview, "b: rerun; reuse 1 successful workers if plan unchanged") || !strings.Contains(preview, "a: reuse successful result") {
		t.Fatal(preview)
	}
	v.HandleKeypress(flowKey("y"))
	if s.from != "" || v.record.Status != flow.RunRunning {
		t.Fatal("resume did not preserve default semantics and refresh")
	}
	v.record.Status = flow.RunFailed
	v.selected = 1
	v.HandleKeypress(flowKey("R"))
	if v.resumeFrom != "b" || strings.Contains(v.resumePreview(), "successful workers") {
		t.Fatal("explicit rerun preview reused partial workers")
	}
	v.HandleKeypress(flowKey("y"))
	if s.from != "b" {
		t.Fatal("explicit rerun did not target selection")
	}
}
func TestFlowEditorEscapeRetainsDraftAndConfirmsClose(t *testing.T) {
	s := flowUIFixture()
	var v FlowView
	v.OpenDraft(s, "sample", flowUISource, flowUISource, 100, 40)
	v.HandleKeypress(flowKey("e"))
	changed := strings.Replace(flowUISource, "Use {{input.topic}}", "Changed {{input.topic}}", 1)
	v.editor.SetValue(changed)
	v.HandleKeypress(flowKey("esc"))
	if v.page != "review" || v.source != changed || v.saved {
		t.Fatal("escape discarded edits or retained runnable state")
	}
	v.HandleKeypress(flowKey("esc"))
	if !v.active || v.confirm != "discard" {
		t.Fatal("unsaved close not confirmed")
	}
	v.HandleKeypress(flowKey("n"))
	if !v.active || v.source != changed {
		t.Fatal("discard cancellation lost source")
	}
	v.HandleKeypress(flowKey("esc"))
	v.HandleKeypress(flowKey("y"))
	if v.active {
		t.Fatal("confirmed discard stayed open")
	}
}
func TestFlowCircularExceptionsAndNeverStartedDetail(t *testing.T) {
	s := flowUIFixture()
	var v FlowView
	v.OpenRun(s, "run", 120, 40)
	for _, status := range []flow.Status{flow.StatusFailed, flow.StatusSkipped, flow.StatusOmitted} {
		v.record.Nodes["c"] = flow.Checkpoint{Outcome: flow.Outcome{Status: status}}
		m := ansi.Strip(v.marker("c"))
		if m != "○" && m != "●" {
			t.Fatalf("noncircular exception marker %q", m)
		}
	}
	v.selected = 2
	if strings.Contains(v.renderDetail(), "0s") {
		t.Fatal("never-started node displays duration")
	}
	live := ansi.Strip(v.Render())
	if strings.Contains(live, "r resume") || !strings.Contains(live, "s stop") {
		t.Fatal("live hints expose retry")
	}
	v.record.Status = flow.RunFailed
	settled := ansi.Strip(v.Render())
	if strings.Contains(settled, "s stop") || !strings.Contains(settled, "r resume") {
		t.Fatal("settled hints expose stop")
	}
}
func TestFlowSourceDiffUsesSharedContext(t *testing.T) {
	var v FlowView
	v.width = 100
	v.name = "sample"
	v.baseline = flowUISource
	v.source = strings.Replace(flowUISource, "Use {{input.topic}}", "Changed {{input.topic}}", 1)
	diff := ansi.Strip(v.diffPreview())
	if !strings.Contains(diff, "- Use {{input.topic}}") || !strings.Contains(diff, "+ Changed {{input.topic}}") || strings.Contains(diff, "+---") {
		t.Fatalf("expected contextual shared diff: %s", diff)
	}
}
