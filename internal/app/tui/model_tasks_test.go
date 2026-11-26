package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/app/tui/input"
	"github.com/genai-io/san/internal/app/tui/transcript"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/todo"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
)

// This worker makes graceful stopping deterministic without launching an agent,
// a subprocess, or the manager's escalation timeout.
type taskViewerWorker struct {
	task.BackgroundTask
	info   task.TaskInfo
	output string
	stops  int
}

func (w *taskViewerWorker) GetID() string                        { return w.info.ID }
func (w *taskViewerWorker) GetStatus() task.TaskInfo             { return w.info }
func (w *taskViewerWorker) GetOutput() string                    { return w.output }
func (w *taskViewerWorker) IsRunning() bool                      { return w.info.Status == task.StatusRunning }
func (w *taskViewerWorker) Stop() error                          { w.stops++; w.info.Status = task.StatusStopped; return nil }
func (w *taskViewerWorker) WaitForCompletion(time.Duration) bool { return !w.IsRunning() }

func viewerWorker(m *model, id, session string, start time.Time) *taskViewerWorker {
	w := &taskViewerWorker{info: task.TaskInfo{ID: id, ParentSessionID: session, StartTime: start, Status: task.StatusRunning, Type: task.TaskTypeAgent, Description: "subject-" + id}}
	m.services.Task.RegisterTask(w)
	return w
}

func TestTaskRootSnapshotsScopeOrderAndUTF8Bound(t *testing.T) {
	m := userPromptModel()
	m.services.sessionID = "current"
	now := time.Now()
	b := viewerWorker(m, "b", "current", now)
	viewerWorker(m, "a", "current", now)
	viewerWorker(m, "old", "other", now.Add(-time.Hour))
	viewerWorker(m, "legacy", "", now.Add(-time.Minute))
	b.output = strings.Repeat("界", taskViewerOutputLimit) + "tail"
	b.info.Progress = "awaiting approval" // Free-form text is not an association.
	for range 10 {
		items := m.taskSnapshots()
		if len(items) != 3 || items[0].ID != "legacy" || items[1].ID != "a" || items[2].ID != "b" {
			t.Fatalf("unstable/scoped snapshots: %+v", items)
		}
		item := items[2]
		if !item.OutputTruncated || len(item.Output) > taskViewerOutputLimit || !utf8.ValidString(item.Output) || !strings.HasSuffix(item.Output, "tail") {
			t.Fatalf("invalid bounded output: bytes=%d valid=%v", len(item.Output), utf8.ValidString(item.Output))
		}
		if item.AwaitingApproval {
			t.Fatal("progress text became permission state")
		}
	}
	b.info.Status = task.StatusCompleted
	if got := m.taskSnapshots(); got[2].ID != "b" {
		t.Fatal("completion reordered task IDs")
	}
}

func TestTaskRootPermissionAssociationsAndMixedFIFOCounts(t *testing.T) {
	m := userPromptModel()
	for _, id := range []string{"current", "queued", "question", "expired", "foreign"} {
		viewerWorker(m, id, "", time.Now())
	}
	expired, cancel := context.WithCancel(context.Background())
	cancel()
	foreign := userPromptApproval(m, context.Background(), "foreign")
	foreign.Owner = transcript.NewAgentToUI(1)
	m.userPrompts.active = userPromptApproval(m, context.Background(), "current")
	m.userPrompts.queue = []*transcript.UserPromptRequest{userPromptQuestion(m, context.Background(), "question"), userPromptApproval(m, context.Background(), "queued"), userPromptApproval(m, expired, "expired"), foreign}
	if n := m.pendingApprovalCount(); n != 2 {
		t.Fatalf("permission count=%d, want 2", n)
	}
	if text := ansi.Strip(m.userPromptQueueContext()); !strings.Contains(text, "1 other approval waiting") {
		t.Fatalf("mixed FIFO count: %q", text)
	}
	for _, item := range m.taskSnapshots() {
		want := item.ID == "current" || item.ID == "queued"
		if item.AwaitingApproval != want || item.ApprovalQueued != (item.ID == "queued") {
			t.Fatalf("wrong association: %+v", item)
		}
	}
}

func TestTaskRootStopIsAsyncExactIDAndRejectsStaleActions(t *testing.T) {
	for _, invalidate := range []string{"reopen", "switch", "reset"} {
		t.Run(invalidate, func(t *testing.T) {
			m := userPromptModel()
			m.services.sessionID = "one"
			selected := viewerWorker(m, "selected", "one", time.Now())
			peer := viewerWorker(m, "peer", "one", time.Now())
			m.openTasks()
			id := m.userInput.Tasks.ViewID()
			cmd := m.stopViewedTask(input.TaskStopMsg{TaskID: "selected", ViewID: id})
			if cmd == nil || selected.stops != 0 || peer.stops != 0 || m.userInput.Tasks.IsActive() {
				t.Fatal("stop blocked UI or ran synchronously")
			}
			switch invalidate {
			case "reopen":
				m.openTasks()
			case "switch":
				m.services.sessionID = "two"
				m.refreshTaskViewer()
			case "reset":
				m.resetUserPrompts()
			}
			active, viewID := m.userInput.Tasks.IsActive(), m.userInput.Tasks.ViewID()
			if m.stopViewedTask(input.TaskStopMsg{TaskID: "peer", ViewID: id}) != nil {
				t.Fatal("stale action accepted")
			}
			result := cmd()
			if selected.stops != 1 || peer.stops != 0 {
				t.Fatal("stop did not target exact worker")
			}
			m.dispatch(result)
			if m.userInput.Tasks.IsActive() != active || m.userInput.Tasks.ViewID() != viewID {
				t.Fatal("late stop completion reopened/replaced viewer")
			}
		})
	}
}

func TestTaskRootStopResultHasNoDuplicateReceipt(t *testing.T) {
	m := userPromptModel()
	m.services.sessionID = "one"
	viewerWorker(m, "worker", "one", time.Now())
	m.openTasks()
	cmd := m.stopViewedTask(input.TaskStopMsg{TaskID: "worker", ViewID: m.userInput.Tasks.ViewID()})
	if cmd == nil {
		t.Fatal("stop action was not accepted")
	}
	m.showNoticeLine(mainNotice{FromAgent: true, Display: "Security review stopped", FinishedTaskID: "worker"})
	m.dispatch(cmd())
	if !m.userInput.Tasks.IsActive() || len(m.conv.Messages) != 1 || m.conv.Messages[0].Content != "Security review stopped" {
		t.Fatalf("stop added a redundant success receipt or failed to return: %+v", m.conv.Messages)
	}
}

func TestTaskRootReviewReturnsSameDetailAfterFIFOQuestion(t *testing.T) {
	for _, invalidated := range []bool{false, true} {
		t.Run(fmt.Sprint(invalidated), func(t *testing.T) {
			m := userPromptModel()
			viewerWorker(m, "first", "", time.Now().Add(-time.Minute))
			viewerWorker(m, "selected", "", time.Now())
			m.openTasks()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			question := userPromptQuestion(m, ctx, "question")
			approval := userPromptApproval(m, context.Background(), "selected")
			m.enqueueUserPrompt(question)
			m.enqueueUserPrompt(approval)
			m.refreshTaskViewer()
			m.userInput.Tasks.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyDown})
			m.userInput.Tasks.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEnter})
			action := m.userInput.Tasks.HandleKeypress(tea.KeyPressMsg{Code: 'r', Text: "r"})
			if action == nil {
				t.Fatal("selected approval was not judgeable")
			}
			m.dispatch(action())
			if m.userPrompts.active != question || !m.conv.Modal.Question.IsActive() || m.userInput.Approval.IsActive() {
				t.Fatal("review promoted queued approval ahead of question")
			}
			if invalidated {
				cancel()
				m.pruneUserPrompts()
			} else {
				m.handleQuestionResponse(transcript.QuestionResponseMsg{Request: question.Question.Request, Response: &tool.QuestionResponse{RequestID: "question", Answers: map[int][]string{0: {"first"}}}})
			}
			rendered := ansi.Strip(m.userInput.Tasks.Render())
			if !m.userInput.Tasks.IsActive() || !strings.Contains(rendered, "ID: selected") || strings.Contains(rendered, "ID: first") {
				t.Fatalf("did not resume same selected detail:\n%s", rendered)
			}
			if m.userInput.Approval.IsActive() {
				t.Fatal("queued approval stole resumed viewer")
			}
		})
	}
}

func TestTaskRootReviewReturnsAfterQueuedHeadExpires(t *testing.T) {
	m := userPromptModel()
	viewerWorker(m, "selected", "", time.Now())
	m.openTasks()
	ctx, cancel := context.WithCancel(context.Background())
	question := userPromptQuestion(m, ctx, "earlier")
	approval := userPromptApproval(m, context.Background(), "selected")
	m.enqueueUserPrompt(question)
	m.enqueueUserPrompt(approval)
	// Model the cancellation boundary after Update's initial prune, before
	// review activation performs its own prune and promotes the next request.
	cancel()
	m.reviewTaskApproval(input.TaskReviewApprovalMsg{TaskID: "selected", ViewID: m.userInput.Tasks.ViewID()})
	if m.userPrompts.active != approval || m.tasks.review != approval {
		t.Fatal("review return path did not follow the surviving FIFO request")
	}
	m.finishUserPrompt()
	if !m.userInput.Tasks.IsActive() {
		t.Fatal("answering the surviving request did not return to the task viewer")
	}
}

type cancelOnRecheckContext struct {
	context.Context
	cancel  context.CancelFunc
	checked bool
}

func (c *cancelOnRecheckContext) Err() error {
	if c.checked {
		c.cancel()
	}
	c.checked = true
	return c.Context.Err()
}

func TestTaskRootReviewResumesWhenQueueExpiresDuringActivation(t *testing.T) {
	m := userPromptModel()
	viewerWorker(m, "selected", "", time.Now())
	m.openTasks()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	approval := userPromptApproval(m, ctx, "selected")
	m.enqueueUserPrompt(approval)
	// Valid for pendingTaskApprovals, then canceled when advanceUserPrompts
	// rechecks it. No request survives to own the return path.
	approval.Context = &cancelOnRecheckContext{Context: ctx, cancel: cancel}
	m.reviewTaskApproval(input.TaskReviewApprovalMsg{TaskID: "selected", ViewID: m.userInput.Tasks.ViewID()})
	if !m.userInput.Tasks.IsActive() || m.userPrompts.active != nil || m.tasks.review != nil {
		t.Fatal("an expired review left the task viewer suspended")
	}
}

func TestTaskRootPeriodicTickScopedRefresh(t *testing.T) {
	m := userPromptModel()
	m.services.sessionID = "one"
	worker := viewerWorker(m, "worker", "one", time.Now())
	if m.openTasks() == nil {
		t.Fatal("viewer has no periodic refresh")
	}
	id := m.tasks.pollID
	if m.taskViewerTick() != nil {
		t.Fatal("duplicate polling command")
	}
	m.dispatch(taskViewerTickMsg{sessionID: "other", viewID: id})
	if m.tasks.pollID != id {
		t.Fatal("foreign tick consumed poll")
	}
	m.dispatch(taskViewerTickMsg{sessionID: "one", viewID: id + 1})
	if m.tasks.pollID != id {
		t.Fatal("old view tick consumed poll")
	}
	worker.info.Description = "periodic-update"
	m.Update(taskViewerTickMsg{sessionID: "one", viewID: id})
	if !strings.Contains(ansi.Strip(m.userInput.Tasks.Render()), "periodic-update") || m.tasks.pollID != id {
		t.Fatal("tick did not refresh and rearm current viewer")
	}
	m.hideTaskViewer()
	m.Update(taskViewerTickMsg{sessionID: "one", viewID: id})
	if m.userInput.Tasks.IsActive() || m.tasks.pollID != 0 {
		t.Fatal("closed viewer rearmed")
	}
}

func TestTaskRootTrackerUsesOnlyLivePermissionAssociation(t *testing.T) {
	m := userPromptModel()
	ctx, cancel := context.WithCancel(task.WithSessionID(context.Background(), m.sessionID()))
	defer cancel()
	worker := m.services.Task.CreateAgentTask("tracked", "Reviewer", "Audit", ctx, cancel)
	item := m.services.Tracker.Create("Audit", "", "", map[string]any{"background_task_id": worker.GetID(), "background_status_detail": "running"})
	if err := m.services.Tracker.Update(item.ID, todo.WithStatus(todo.StatusInProgress)); err != nil {
		t.Fatal(err)
	}
	m.userPrompts.active = userPromptQuestion(m, context.Background(), worker.GetID())
	if rows, _ := m.renderTrackerList(); strings.Contains(ansi.Strip(rows), "Awaiting approval") {
		t.Fatal("question marked tracker as awaiting approval")
	}
	permissionCtx, expire := context.WithCancel(context.Background())
	m.userPrompts.queue = []*transcript.UserPromptRequest{userPromptApproval(m, permissionCtx, worker.GetID())}
	if rows, _ := m.renderTrackerList(); !strings.Contains(ansi.Strip(rows), "Awaiting approval") {
		t.Fatal("queued valid permission not reflected in tracker")
	}
	expire()
	if rows, _ := m.renderTrackerList(); strings.Contains(ansi.Strip(rows), "Awaiting approval") {
		t.Fatal("expired permission remained in tracker")
	}
}

func TestTaskRootQueuedMainApprovalKeepsOperationInspectable(t *testing.T) {
	for _, width := range []int{32, 40} {
		for _, height := range []int{8, 10, 12} {
			t.Run(fmt.Sprintf("%dx%d", width, height), func(t *testing.T) {
				m := userPromptModel()
				m.env.Width, m.env.Height = width, height
				current := userPromptApproval(m, context.Background(), "current")
				current.Source.AgentID = ""
				current.Permission.ToolName = "Bash"
				current.PermissionView = &perm.PermissionRequest{ToolName: "Bash", BashMeta: &perm.BashMetadata{Command: "operation-first\noperation-second\noperation-last"}}
				m.enqueueUserPrompt(current)
				m.enqueueUserPrompt(userPromptApproval(m, context.Background(), "queued"))
				frame := ansi.Strip(m.View().Content)
				if !strings.Contains(frame, "operation-first") {
					t.Fatalf("actionable approval hid the operation:\n%s", frame)
				}
				seen := frame
				for range 10 {
					m.userInput.Approval.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyPgDown})
					frame = ansi.Strip(m.View().Content)
					seen += frame
					if rowCount(frame) > height || !strings.Contains(frame, "Allow once") || !strings.Contains(frame, "Deny") {
						t.Fatalf("scroll displaced decisions:\n%s", frame)
					}
				}
				for _, operation := range []string{"operation-first", "operation-second", "operation-last"} {
					if !strings.Contains(seen, operation) {
						t.Fatalf("operation row unreachable: %s", operation)
					}
				}
			})
		}
	}
}

func TestTaskRootDockedApprovalFitsSmallTerminalWithQueue(t *testing.T) {
	for _, width := range []int{32, 40} {
		for _, height := range []int{10, 12, 24} {
			for _, queued := range []bool{false, true} {
				t.Run(fmt.Sprintf("%dx%d/queued=%v", width, height, queued), func(t *testing.T) {
					m := userPromptModel()
					m.env.Width = width
					m.env.Height = height
					current := userPromptApproval(m, context.Background(), "current")
					m.enqueueUserPrompt(current)
					if queued {
						m.enqueueUserPrompt(userPromptApproval(m, context.Background(), "queued"))
					}
					frame := m.View().Content
					if rows := strings.Count(frame, "\n") + 1; rows > height {
						t.Fatalf("approval frame overflows: %d > %d\n%s", rows, height, ansi.Strip(frame))
					}
					for _, line := range strings.Split(frame, "\n") {
						if n := ansi.StringWidth(line); n >= width {
							t.Fatalf("approval reaches wrap column: %d >= %d: %q", n, width, ansi.Strip(line))
						}
					}
					if !strings.Contains(ansi.Strip(frame), "Write") {
						t.Fatalf("approval missing: %s", ansi.Strip(frame))
					}
				})
			}
		}
	}
}
