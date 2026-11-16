package app

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/flow"
	"github.com/genai-io/san/internal/task"
)

type flowTaskService struct {
	input.FlowService
	record flow.RunRecord
}

func (s flowTaskService) InspectRun(string) (*flow.RunRecord, error) {
	return &s.record, nil
}

func TestFlowChildUsesNativeTaskOverlayAndReturns(t *testing.T) {
	manager := task.NewManager()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	child := manager.CreateForegroundAgentTask("child", "Explore", "Inspect package", ctx, cancel)
	child.SetIdentity("Explore", "child-session")
	child.Complete(nil)
	m := flowInputModel(manager)
	m.env.Height = 30
	s := flowTaskService{record: flow.RunRecord{
		ID: "run", Status: flow.RunSucceeded,
		Definition: "```mermaid\nflowchart LR\n a\n```\n## a\nInspect\n",
		Nodes:      map[string]flow.Checkpoint{"a": {Status: flow.StatusSucceeded, Execution: flow.Execution{TaskID: "child", SessionID: "child-session"}}},
	}}
	m.userInput.Flow.OpenRun(s, "run", 80, 30)
	cmd := m.userInput.Flow.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("node did not request native inspection")
	}
	m.dispatch(cmd())
	panel, ok := m.activeOverlay()
	if !ok || panel != &m.userInput.Tasks || !strings.Contains(panel.Render(), "Owner: Explore") {
		t.Fatal("Flow inspector obscured native task detail")
	}
	escape := tea.KeyPressMsg{Code: tea.KeyEscape}
	m.userInput.Tasks.HandleKeypress(escape)
	closeCmd := m.userInput.Tasks.HandleKeypress(escape)
	if closeCmd == nil {
		t.Fatal("native task viewer did not close")
	}
	m.dispatch(closeCmd())
	if panel, ok := m.activeOverlay(); !ok || panel != &m.userInput.Flow {
		t.Fatal("closing native task detail lost the Flow inspector")
	}
}

func TestFlowStartArmsTaskAreaClock(t *testing.T) {
	m := flowInputModel(task.NewManager())
	if _, cmd := m.dispatch(input.FlowStartedMsg{}); cmd == nil {
		t.Fatal("Flow launch needs a frame clock without opening an overlay")
	}
	if _, active := m.activeOverlay(); active {
		t.Fatal("starting the task-area clock opened an overlay")
	}
}

func TestManagedFlowChildCannotNativeContinue(t *testing.T) {
	info := task.TaskInfo{Type: task.TaskTypeAgent, AgentSessionID: "child", Status: task.StatusCompleted}
	if !canContinueTask(info) {
		t.Fatal("ordinary task continuation changed")
	}
	info.ParentTaskID = "flow-parent"
	if canContinueTask(info) {
		t.Fatal("managed child must use graph recovery")
	}
}
