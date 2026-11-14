package input

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/workflow"
)

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
