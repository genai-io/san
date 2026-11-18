package input

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/flow"
)

func TestFlowMainExecutionUsesMessageIdentity(t *testing.T) {
	s := flowUIFixture()
	c := s.r.Nodes["b"]
	c.Execution = flow.Execution{SessionID: "main-session", MessageID: "writer-input"}
	s.r.Nodes["b"] = c
	var v FlowView
	v.OpenRun(s, "run", 100, 40)
	v.SyncSession("main-session")
	cmd := v.HandleKeypress(flowKey("enter"))
	if cmd == nil || !v.canInspect() {
		t.Fatal("main execution did not offer native inspection")
	}
	msg := cmd().(FlowOpenTaskMsg)
	if msg.Execution != c.Execution || !v.AcceptTaskOpen(msg) || !strings.Contains(v.executionChoices()[0].label, "main message writer-input") {
		t.Fatal("main execution lost its actual message identity")
	}
	if _, ok := v.RecordedTask(msg); ok {
		t.Fatal("main message was fabricated as a child task")
	}
	for _, want := range []string{"Open native history", "Enter native history"} {
		if got := ansi.Strip(v.Render()); !strings.Contains(got, want) {
			t.Fatalf("main execution inspection mislabeled: %s", got)
		}
	}
	msg.Execution.MessageID = "forged"
	if v.AcceptTaskOpen(msg) {
		t.Fatal("different main turn accepted")
	}
}

func TestFlowNativeExecutionRouting(t *testing.T) {
	s := flowUIFixture()
	c := s.r.Nodes["b"]
	c.Execution = flow.Execution{TaskID: "child", SessionID: "child-session"}
	c.History = []flow.Attempt{{Outcome: flow.Outcome{Execution: flow.Execution{TaskID: "old-child", SessionID: "old-session"}}, Number: 1}}
	s.r.Nodes["b"] = c
	var v FlowView
	v.OpenRun(s, "run", 100, 40)
	v.SyncSession("parent")
	v.HandleKeypress(flowKey("enter"))
	cmd := v.HandleKeypress(flowKey("enter"))
	msg := cmd().(FlowOpenTaskMsg)
	if msg.Execution.TaskID != "child" || !v.AcceptTaskOpen(msg) {
		t.Fatal(msg)
	}
	v.HandleKeypress(flowKey("down"))
	msg = v.HandleKeypress(flowKey("enter"))().(FlowOpenTaskMsg)
	if msg.Execution.TaskID != "old-child" {
		t.Fatal(msg)
	}
	v.SyncSession("other-parent")
	if v.AcceptTaskOpen(msg) {
		t.Fatal("stale session accepted")
	}
}

func TestFlowHistoricalWorkersUseOriginalExecutions(t *testing.T) {
	s := flowUIFixture()
	c := s.r.Nodes["b"]
	c.Execution = flow.Execution{}
	c.History = []flow.Attempt{{Outcome: flow.Outcome{Workers: []flow.WorkerCheckpoint{
		{Index: 0, Label: "same", State: flow.Checkpoint{Outcome: flow.Outcome{Execution: flow.Execution{TaskID: "old-one", SessionID: "one-session"}}, Attempt: 1}},
		{Index: 1, Label: "same", State: flow.Checkpoint{Outcome: flow.Outcome{Execution: flow.Execution{TaskID: "old-two", SessionID: "two-session"}}, Attempt: 1}},
	}}, Number: 1}}
	s.r.Nodes["b"] = c
	var v FlowView
	v.OpenRun(s, "run", 100, 40)
	v.HandleKeypress(flowKey("enter"))
	if choices := v.executionChoices(); len(choices) != 2 || !strings.Contains(choices[1].label, "Worker 2") {
		t.Fatal("historical worker choices should retain their original indices")
	}
	v.HandleKeypress(flowKey("down"))
	msg := v.HandleKeypress(flowKey("enter"))().(FlowOpenTaskMsg)
	if msg.Execution.TaskID != "old-two" || msg.Execution.SessionID != "two-session" {
		t.Fatal("historical worker selection opened a different execution")
	}
}

func TestFlowUnavailableExecutionDoesNotLaunch(t *testing.T) {
	s := flowUIFixture()
	var v FlowView
	v.OpenRun(s, "run", 100, 40)
	v.HandleKeypress(flowKey("enter"))
	if v.HandleKeypress(flowKey("enter")) != nil || !strings.Contains(v.message, "unavailable") || s.launches != 0 {
		t.Fatal("old record inspection must be inert")
	}
}

func TestTaskViewerShowExactExecution(t *testing.T) {
	var v TaskViewer
	items := []TaskViewItem{{ID: "child", SessionID: "right"}, {ID: "other", SessionID: "other"}}
	if v.ShowTask(items, "child", "wrong", 80, 30) || v.IsActive() {
		t.Fatal("reused identity accepted")
	}
	if !v.ShowTask(items, "child", "right", 80, 30) || !v.detail || v.selected != "child" {
		t.Fatal("native detail not selected")
	}
	if v.ShowTask(items, "missing", "right", 80, 30) {
		t.Fatal("missing execution opened unrelated task")
	}
}
