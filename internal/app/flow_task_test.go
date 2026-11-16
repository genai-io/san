package app

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/flow"
	"github.com/genai-io/san/internal/task"
)

type flowTaskService struct {
	input.FlowService
	record flow.RunRecord
}

func (s flowTaskService) GetRun(string) (*flow.RunRecord, error) {
	return &s.record, nil
}

func TestFlowMainStepOpensNativeHistoryAndReturns(t *testing.T) {
	m := flowInputModel(nil)
	m.services.sessionID = "main-session"
	m.env.Height = 30
	m.conv.Append(core.ChatMessage{ID: "writer-input", Role: core.ChatUser, Content: "WRITER_NATIVE_INPUT"})
	m.conv.Append(core.ChatMessage{ID: "writer-answer", Role: core.ChatAssistant, Content: "WRITER_NATIVE_ANSWER"})
	s := flowTaskService{record: flow.RunRecord{ID: "run", Status: flow.RunSucceeded,
		Definition: "```mermaid\nflowchart LR\n writer\n```\n## writer\nexecutor: main\n\nWrite\n",
		Nodes: map[string]flow.Checkpoint{"writer": {Outcome: flow.Outcome{Status: flow.StatusSucceeded,
			Execution: flow.Execution{SessionID: "main-session", MessageID: "writer-input"}}}}}}
	m.userInput.Flow.OpenRun(s, "run", 80, 30)
	open := m.userInput.Flow.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEnter})
	if open == nil {
		t.Fatal("main node did not request inspection")
	}
	msg := open().(input.FlowOpenTaskMsg)
	foreign := msg
	foreign.Execution.SessionID = "other"
	m.dispatch(foreign)
	if m.userInput.Transcript.IsActive() {
		t.Fatal("foreign main identity opened history")
	}
	_, load := m.dispatch(msg)
	if load == nil {
		t.Fatal("main identity did not open native history")
	}
	m.dispatch(load())
	panel, active := m.activeOverlay()
	view := ansi.Strip(m.userInput.Transcript.Render())
	if !active || panel != &m.userInput.Transcript || !strings.Contains(view, "WRITER_NATIVE_INPUT") || !strings.Contains(view, "WRITER_NATIVE_ANSWER") || m.userInput.Tasks.IsActive() {
		t.Fatalf("main execution did not use exact native conversation history:\n%s", view)
	}
	m.userInput.Transcript.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEscape})
	if panel, active := m.activeOverlay(); !active || panel != &m.userInput.Flow {
		t.Fatal("closing native main history lost the Flow inspector")
	}
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
		Nodes:      map[string]flow.Checkpoint{"a": {Outcome: flow.Outcome{Status: flow.StatusSucceeded, Execution: flow.Execution{TaskID: "child", SessionID: "child-session"}}}},
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

func TestFlowRecordedCheckpointInspection(t *testing.T) {
	old := flow.Execution{TaskID: "old-child", SessionID: "old-session"}
	current := flow.Execution{TaskID: "child", SessionID: "child-session"}
	for _, historical := range []bool{false, true} {
		t.Run(map[bool]string{false: "completed", true: "historical-fanout"}[historical], func(t *testing.T) {
			manager := task.NewManager()
			m := flowInputModel(manager)
			m.env.Height = 35
			answer := "checkpoint answer " + strings.Repeat("full answer\n", 4000)
			state := flow.Checkpoint{Outcome: flow.Outcome{Status: flow.StatusSucceeded, Execution: current, Output: answer}, Attempt: 2}
			want := current
			if historical {
				state = flow.Checkpoint{Outcome: flow.Outcome{Status: flow.StatusPending}, History: []flow.Attempt{{Outcome: flow.Outcome{Workers: []flow.WorkerCheckpoint{{State: flow.Checkpoint{Outcome: flow.Outcome{Execution: old, Status: flow.StatusFailed, Output: answer, Error: "old failure"}, Attempt: 1}}}}, Number: 2}}}
				want = old
			}
			s := flowTaskService{record: flow.RunRecord{ID: "run", Status: flow.RunSucceeded,
				Definition: "```mermaid\nflowchart LR\n a\n```\n## a\nInspect\n", Nodes: map[string]flow.Checkpoint{"a": state}}}
			m.userInput.Flow.OpenRun(s, "run", 80, 35)
			cmd := m.userInput.Flow.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEnter})
			if cmd == nil {
				t.Fatal("missing inspection event")
			}
			msg := cmd().(input.FlowOpenTaskMsg)
			if msg.Execution != want {
				t.Fatalf("wrong execution: %+v", msg)
			}
			forged := msg
			forged.Execution.SessionID = "forged"
			m.dispatch(forged)
			if m.userInput.Tasks.IsActive() {
				t.Fatal("forged identity accepted")
			}
			stale := msg
			stale.Generation++
			m.dispatch(stale)
			if m.userInput.Tasks.IsActive() {
				t.Fatal("stale generation accepted")
			}
			m.dispatch(msg)
			if !m.userInput.Tasks.IsActive() || m.tasks.recorded == nil {
				t.Fatal("recorded checkpoint did not open")
			}
			m.refreshTaskViewer()
			items := m.taskSnapshots()
			if len(items) != 1 || items[0].Output != answer || !items[0].Recorded || items[0].CanContinue || items[0].SessionID != want.SessionID {
				t.Fatal("refresh lost exact full recorded answer")
			}
			if len(manager.List()) != 0 {
				t.Fatal("inspection registered a task")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			collision := manager.CreateForegroundAgentTask(want.TaskID, "Other", "Unrelated task", ctx, cancel)
			collision.SetIdentity("Other", "other-session")
			collision.AppendOutput([]byte("unrelated output"))
			collision.Complete(nil)
			m.refreshTaskViewer()
			items = m.taskSnapshots()
			if len(items) != 1 || items[0].Output != answer || items[0].SessionID != want.SessionID || !items[0].Recorded {
				t.Fatal("runtime ID collision replaced the recorded inspection")
			}
			if m.continueViewedTask(input.TaskContinueMsg{TaskID: want.TaskID, ViewID: m.tasks.actionID}) != nil ||
				m.stopViewedTask(input.TaskStopMsg{TaskID: want.TaskID, ViewID: m.tasks.actionID}) != nil ||
				m.reviewTaskApproval(input.TaskReviewApprovalMsg{TaskID: want.TaskID, ViewID: m.tasks.actionID}) != nil {
				t.Fatal("recorded inspection accepted a lifecycle action")
			}
			escape := tea.KeyPressMsg{Code: tea.KeyEscape}
			m.userInput.Tasks.HandleKeypress(escape)
			closeCmd := m.userInput.Tasks.HandleKeypress(escape)
			if closeCmd == nil {
				t.Fatal("missing close")
			}
			m.dispatch(closeCmd())
			if panel, ok := m.activeOverlay(); !ok || panel != &m.userInput.Flow || m.tasks.recorded != nil {
				t.Fatal("close did not return to Flow")
			}
		})
	}
}

func TestFlowRecordedRejectsConflictingLiveIdentity(t *testing.T) {
	{
		manager := task.NewManager()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		child := manager.CreateForegroundAgentTask("child", "Explore", "Inspect", ctx, cancel)
		child.SetIdentity("Explore", "wrong-session")
		child.Complete(nil)
		m := flowInputModel(manager)
		s := flowTaskService{record: flow.RunRecord{ID: "run", Status: flow.RunSucceeded,
			Definition: "```mermaid\nflowchart LR\n a\n```\n## a\nInspect\n",
			Nodes:      map[string]flow.Checkpoint{"a": {Outcome: flow.Outcome{Status: flow.StatusSucceeded, Output: "saved", Execution: flow.Execution{TaskID: "child", SessionID: "saved-session"}}}}}}
		m.userInput.Flow.OpenRun(s, "run", 80, 30)
		cmd := m.userInput.Flow.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEnter})
		m.dispatch(cmd())
		if m.userInput.Tasks.IsActive() || m.tasks.recorded != nil {
			t.Fatal("conflicting live identity fell back to checkpoint")
		}
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
