package input

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/workflow"
)

func TestWorkflowReviewExplainsInputsAndStepResults(t *testing.T) {
	c := workflowController(t, nil)
	c.env.Input, c.env.Width, c.env.Height = &Model{}, 96, 32
	if _, _, err := c.handleWorkflowCommand(context.Background(), "new wf-smoke"); err != nil {
		t.Fatal(err)
	}
	v := &c.env.Input.Workflow
	initial := ansi.Strip(v.Render())
	for _, want := range []string{"Run inputs", "task — you provide this before running", "{{input.task}} ← task you enter", "{{work}} = this step's final answer", "Read by review"} {
		if !strings.Contains(initial, want) {
			t.Fatalf("missing %q in review:\n%s", want, initial)
		}
	}
	for _, unwanted := range []string{"Draft — save a valid", "Save project workflow", "Project copy:", "nodes · up to", "max_parallel", "```mermaid"} {
		if strings.Contains(initial, unwanted) {
			t.Fatalf("review exposes %q:\n%s", unwanted, initial)
		}
	}
	v.HandleKeypress(workflowKey("down"))
	if v.nodeID() != "review" || !strings.Contains(ansi.Strip(v.Render()), "{{work}} ← final answer from step work") {
		t.Fatal("selecting a step did not reveal where its input comes from")
	}
	if !strings.Contains(ansi.Strip(v.Render()), "Final workflow result") {
		t.Fatal("sink output is not identified as the workflow result")
	}
	v.HandleKeypress(workflowKey("d"))
	limits := ansi.Strip(v.Render())
	if v.page != "details" || !strings.Contains(limits, "At most 2 step executions") || !strings.Contains(limits, "At most 1 step running") {
		t.Fatal("execution limits are missing or the starter is unnecessarily parallel")
	}
	v.HandleKeypress(workflowKey("esc"))
	if v.page != "review" || !v.active || v.confirm != "" {
		t.Fatal("leaving details closed the unsaved draft")
	}
	v.HandleKeypress(workflowKey("e"))
	edit := ansi.Strip(v.Render())
	if !strings.Contains(edit, "{{input.task}} = values you enter before running") || !strings.Contains(edit, "step answers, available downstream") {
		t.Fatal("source editor does not explain template values")
	}
}

func TestWorkflowReviewExplainsWorkerDataFlow(t *testing.T) {
	source := workflowDraft("sample", "plan --> work --> result", "## plan\nReturn a JSON object with a tasks array.\n\n## work\nfor_each: plan.tasks\nmax_workers: 2\n\nDo {{item.task}}\n\n## result\nCombine {{work}}\n")
	var v WorkflowView
	v.OpenDraft(workflowUIFixture(), "sample", source, "", 96, 32)
	if !strings.Contains(v.renderReview(), "Read by work") {
		t.Fatal("plan does not identify the step reading its array")
	}
	v.HandleKeypress(workflowKey("down"))
	for _, want := range []string{"Items   JSON array from plan.tasks", "{{item.task}} ← value from the current plan item", "combined answers from completed workers", "Read by result"} {
		if !strings.Contains(v.renderReview(), want) {
			t.Fatalf("worker review does not explain %q", want)
		}
	}
}

func TestWorkflowReviewKeepsPrimaryActionsVisible(t *testing.T) {
	for _, saved := range []bool{false, true} {
		var v WorkflowView
		baseline := ""
		if saved {
			baseline = workflowUISource
		}
		v.OpenDraft(workflowUIFixture(), "sample", workflowUISource, baseline, 40, 24)
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

func TestWorkflowReviewSelectionAfterRemovingSteps(t *testing.T) {
	for _, action := range []string{"esc", "ctrl+s"} {
		t.Run(action, func(t *testing.T) {
			var v WorkflowView
			v.OpenDraft(workflowUIFixture(), "sample", workflowUISource, workflowUISource, 96, 32)
			v.selected = 3
			v.HandleKeypress(workflowKey("e"))
			v.editor.SetValue(workflowDraft("sample", "a", "## a\nUse {{input.topic}}\n"))
			v.HandleKeypress(workflowKey(action))
			if action == "ctrl+s" {
				v.HandleKeypress(workflowKey("y"))
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

func TestWorkflowResumeModesAndWorkerPreview(t *testing.T) {
	s := workflowUIFixture()
	s.r.Status = workflow.RunFailed
	c := s.r.Nodes["b"]
	c.Status = workflow.StatusFailed
	c.Workers = []workflow.WorkerCheckpoint{{State: workflow.Checkpoint{Status: workflow.StatusSucceeded}}}
	s.r.Nodes["b"] = c
	var v WorkflowView
	v.OpenRun(s, "run", 110, 40)
	if v.nodeID() != "b" {
		t.Fatal("failed run should focus its failed node")
	}
	v.selected, v.follow = 3, false
	v.HandleKeypress(workflowKey("r"))
	if v.nodeID() != "d" || v.resumeFrom != "" {
		t.Fatal("default resume targeted selection")
	}
	preview := v.resumePreview()
	if !strings.Contains(preview, "b: rerun; reuse 1 successful workers if plan unchanged") || !strings.Contains(preview, "a: reuse successful result") {
		t.Fatal(preview)
	}
	v.HandleKeypress(workflowKey("y"))
	if s.from != "" || v.record.Status != workflow.RunRunning {
		t.Fatal("resume did not preserve default semantics and refresh")
	}
	v.record.Status = workflow.RunFailed
	v.selected = 1
	v.HandleKeypress(workflowKey("R"))
	if v.resumeFrom != "b" || strings.Contains(v.resumePreview(), "successful workers") {
		t.Fatal("explicit rerun preview reused partial workers")
	}
	v.HandleKeypress(workflowKey("y"))
	if s.from != "b" {
		t.Fatal("explicit rerun did not target selection")
	}
}
func TestWorkflowEditorEscapeRetainsDraftAndConfirmsClose(t *testing.T) {
	s := workflowUIFixture()
	var v WorkflowView
	v.OpenDraft(s, "sample", workflowUISource, workflowUISource, 100, 40)
	v.HandleKeypress(workflowKey("e"))
	changed := strings.Replace(workflowUISource, "Use {{input.topic}}", "Changed {{input.topic}}", 1)
	v.editor.SetValue(changed)
	v.HandleKeypress(workflowKey("esc"))
	if v.page != "review" || v.source != changed || v.saved {
		t.Fatal("escape discarded edits or retained runnable state")
	}
	v.HandleKeypress(workflowKey("esc"))
	if !v.active || v.confirm != "discard" {
		t.Fatal("unsaved close not confirmed")
	}
	v.HandleKeypress(workflowKey("n"))
	if !v.active || v.source != changed {
		t.Fatal("discard cancellation lost source")
	}
	v.HandleKeypress(workflowKey("esc"))
	v.HandleKeypress(workflowKey("y"))
	if v.active {
		t.Fatal("confirmed discard stayed open")
	}
}
func TestWorkflowCircularExceptionsAndNeverStartedDetail(t *testing.T) {
	s := workflowUIFixture()
	var v WorkflowView
	v.OpenRun(s, "run", 120, 40)
	for _, status := range []workflow.Status{workflow.StatusFailed, workflow.StatusSkipped, workflow.StatusOmitted} {
		v.record.Nodes["c"] = workflow.Checkpoint{Status: status}
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
	v.record.Status = workflow.RunFailed
	settled := ansi.Strip(v.Render())
	if strings.Contains(settled, "s stop") || !strings.Contains(settled, "r resume") {
		t.Fatal("settled hints expose stop")
	}
}
func TestWorkflowSourceDiffUsesSharedContext(t *testing.T) {
	var v WorkflowView
	v.width = 100
	v.name = "sample"
	v.baseline = workflowUISource
	v.source = strings.Replace(workflowUISource, "Use {{input.topic}}", "Changed {{input.topic}}", 1)
	diff := ansi.Strip(v.diffPreview())
	if !strings.Contains(diff, "- Use {{input.topic}}") || !strings.Contains(diff, "+ Changed {{input.topic}}") || strings.Contains(diff, "+---") {
		t.Fatalf("expected contextual shared diff: %s", diff)
	}
}
