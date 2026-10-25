package app

import (
	"errors"
	"fmt"
	"html"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/task"
)

const taskViewerOutputLimit = 32 * 1024

type taskViewerState struct {
	sessionID                string
	actionID, pollID, stopID uint64
	review                   *conv.InteractionRequest
}

type taskViewerTickMsg struct {
	sessionID string
	viewID    uint64
}
type taskStopFinishedMsg struct {
	sessionID string
	taskID    string
	viewID    uint64
	err       error
}

// pendingTaskApprovals derives waiting state from actual, valid permission
// requests, not task status or a worker's free-form progress text.
func (m *model) pendingTaskApprovals() map[string]bool {
	waiting := make(map[string]bool)
	add := func(request *conv.InteractionRequest) {
		if m.validInteraction(request) && request.Permission != nil && request.Source.TaskID != "" {
			waiting[request.Source.TaskID] = true
		}
	}
	add(m.interactions.active)
	for _, request := range m.interactions.queue {
		add(request)
	}
	return waiting
}

func (m *model) pendingApprovalCount() int {
	count := 0
	if request := m.interactions.active; m.validInteraction(request) && request.Permission != nil {
		count++
	}
	for _, request := range m.interactions.queue {
		if m.validInteraction(request) && request.Permission != nil {
			count++
		}
	}
	return count
}

func (m *model) taskSnapshots() []input.TaskViewItem {
	if m.services.Task == nil {
		return nil
	}
	workers := m.services.Task.List()
	infos := make([]task.TaskInfo, 0, len(workers))
	sessionID := m.sessionID()
	for _, worker := range workers {
		info := worker.GetStatus()
		if info.ParentSessionID != "" && info.ParentSessionID != sessionID {
			continue
		}
		if info.Output == "" {
			info.Output = worker.GetOutput()
		}
		infos = append(infos, info)
	}
	// Status includes copied output. Capture it once, then sort snapshots rather
	// than copying every worker's buffer on each comparison.
	sort.Slice(infos, func(i, j int) bool {
		a, b := infos[i], infos[j]
		if a.StartTime.Equal(b.StartTime) {
			return a.ID < b.ID
		}
		return a.StartTime.Before(b.StartTime)
	})
	waiting := m.pendingTaskApprovals()
	activeTask := ""
	if request := m.interactions.active; m.validInteraction(request) && request.Permission != nil {
		activeTask = request.Source.TaskID
	}
	items := make([]input.TaskViewItem, 0, len(infos))
	for _, info := range infos {
		output := info.Output
		truncated := len(output) > taskViewerOutputLimit
		if truncated {
			output = output[len(output)-taskViewerOutputLimit:]
			// Avoid starting the bounded preview in the middle of a UTF-8 rune.
			for len(output) > 0 && output[0]&0xc0 == 0x80 {
				output = output[1:]
			}
			output = strings.Clone(output)
		}
		owner := info.AgentName
		if owner == "" {
			owner = string(info.Type)
		}
		progress := info.Progress
		if info.OutputError != "" {
			progress = strings.TrimSpace("Output log unavailable: " + info.OutputError + " · " + progress)
		}
		history := make([]string, 0, len(info.Attempts))
		for _, attempt := range info.Attempts {
			line := fmt.Sprintf("%d %s", attempt.Attempt, attempt.Status)
			if attempt.Error != "" {
				reason, _, _ := strings.Cut(attempt.Error, "\n")
				line += ": " + reason
			}
			history = append(history, line)
		}
		items = append(items, input.TaskViewItem{
			ID: info.ID, AgentName: owner, Description: info.Description,
			Subject: info.Description, Status: info.Status, Progress: progress,
			Output: output, Error: info.Error, OutputTruncated: truncated,
			CanContinue: canContinueTask(info), Attempt: info.Attempt, SessionID: info.AgentSessionID, Attempts: strings.Join(history, "\n"),
			AwaitingApproval: info.Status == task.StatusRunning && waiting[info.ID],
			ApprovalQueued:   waiting[info.ID] && activeTask != info.ID,
		})
	}
	return items
}

func canContinueTask(info task.TaskInfo) bool {
	return info.Type == task.TaskTypeAgent && info.AgentSessionID != "" && (info.Status == task.StatusFailed || info.Status == task.StatusCompleted) && task.RecoveryBudgetAvailable(info.Attempts)
}

// continueViewedTask asks main to choose a safe incremental instruction. This
// never directly launches a worker or bypasses the Agent tool's permission gate.
func (m *model) continueViewedTask(msg input.TaskContinueMsg) tea.Cmd {
	if !m.consumeTaskAction(msg.ViewID) {
		return nil
	}
	if m.services.Task == nil {
		m.resumeTaskViewer()
		return nil
	}
	worker, exists := m.services.Task.Get(msg.TaskID)
	if !exists {
		m.resumeTaskViewer()
		return nil
	}
	info := worker.GetStatus()
	if !canContinueTask(info) || info.Attempt != msg.Attempt || (info.ParentSessionID != "" && info.ParentSessionID != m.sessionID()) {
		m.resumeTaskViewer()
		return nil
	}
	m.hideTaskViewer()
	body := fmt.Sprintf("The user requests continuing task %q (%s) in its original child session. The latest execution ended with status %s.\n", info.ID, info.Description, info.Status)
	if info.Error != "" {
		body += "Failure: " + info.Error + "\n"
	}
	body += "Choose a safe continuation based on the available summary. Use Agent with resume_task_id set to this task ID and only a short corrective/follow-up instruction. Its original conversation is restored internally; do not copy the full child transcript into the main context. Check uncertain side effects before retrying and never repeat denied commands. If continuing is unsafe or unavailable, explain the blocker instead."
	content := fmt.Sprintf("<agent-message from=\"task-control\" subject=\"Continue task %s\">\n%s\n</agent-message>", html.EscapeString(info.ID), html.EscapeString(body))
	message := m.showNoticeLine(mainNotice{FromAgent: true, Content: content})
	return m.SubmitToAgent(message)
}

func (m *model) openTasks() tea.Cmd {
	m.tasks = taskViewerState{sessionID: m.sessionID()}
	m.userInput.Tasks.Show(m.taskSnapshots(), m.env.Width, m.env.Height)
	m.tasks.actionID = m.userInput.Tasks.ViewID()
	return m.taskViewerTick()
}

func (m *model) taskViewerTick() tea.Cmd {
	if !m.userInput.Tasks.IsActive() || m.tasks.pollID != 0 {
		return nil
	}
	sessionID, viewID := m.tasks.sessionID, m.userInput.Tasks.ViewID()
	m.tasks.pollID = viewID
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return taskViewerTickMsg{sessionID, viewID} })
}

func (m *model) hideTaskViewer() {
	m.userInput.Tasks.Hide()
	m.tasks = taskViewerState{}
}

func (m *model) resumeTaskViewer() {
	if m.tasks.sessionID != m.sessionID() {
		m.hideTaskViewer()
		return
	}
	m.userInput.Tasks.Resume(m.taskSnapshots(), m.env.Width, m.env.Height)
	m.tasks.actionID = m.userInput.Tasks.ViewID()
	m.tasks.pollID, m.tasks.stopID = 0, 0
	m.tasks.review = nil
}

// syncTaskViewerSession invalidates a viewer after a session switch without
// copying worker output or rebuilding the viewport on unrelated UI messages.
func (m *model) syncTaskViewerSession() {
	if m.tasks.sessionID != m.sessionID() && (m.tasks != (taskViewerState{}) || m.userInput.Tasks.IsActive() || m.userInput.Tasks.ActionPending()) {
		m.hideTaskViewer()
	}
}

func (m *model) refreshTaskViewer() {
	m.syncTaskViewerSession()
	if m.userInput.Tasks.IsActive() {
		m.userInput.Tasks.Refresh(m.taskSnapshots())
	}
}

// consumeTaskAction rejects delayed actions from a closed/replaced viewer, even
// if the user has reopened /tasks in the same session in the meantime.
func (m *model) consumeTaskAction(viewID uint64) bool {
	if viewID == 0 || viewID != m.tasks.actionID || m.tasks.sessionID != m.sessionID() {
		return false
	}
	m.tasks.actionID = 0
	m.userInput.Tasks.Suspend()
	return true
}

func (m *model) reviewTaskApproval(msg input.TaskReviewApprovalMsg) tea.Cmd {
	if !m.consumeTaskAction(msg.ViewID) {
		return nil
	}
	if !m.pendingTaskApprovals()[msg.TaskID] {
		m.resumeTaskViewer()
		return nil
	}
	// Reveal the existing mixed FIFO, never promote the selected worker. Pruning
	// can expire its head, so bind the return path to the request actually active.
	m.tasks.review = nil
	cmd := m.advanceInteractions()
	m.tasks.review = m.interactions.active
	if m.tasks.review == nil {
		m.resumeTaskViewer()
	}
	return cmd
}

func (m *model) stopViewedTask(msg input.TaskStopMsg) tea.Cmd {
	if !m.consumeTaskAction(msg.ViewID) {
		return nil
	}
	if m.services.Task == nil {
		m.resumeTaskViewer()
		return nil
	}
	worker, known := m.services.Task.Get(msg.TaskID)
	if !known {
		m.resumeTaskViewer()
		return nil
	}
	info := worker.GetStatus()
	if info.ParentSessionID != "" && info.ParentSessionID != m.sessionID() || !worker.IsRunning() || info.Attempt != msg.Attempt {
		m.resumeTaskViewer()
		return nil
	}
	manager, sessionID := m.services.Task, m.sessionID()
	m.tasks.stopID = msg.ViewID
	return func() tea.Msg {
		current, exists := manager.Get(msg.TaskID)
		var err error
		if !exists || current != worker {
			err = errors.New("task is no longer the selected worker")
		} else if _, agent := worker.(*task.AgentTask); agent {
			err = manager.KillAttempt(msg.TaskID, info.Attempt)
		} else {
			err = manager.Kill(msg.TaskID)
		}
		return taskStopFinishedMsg{sessionID: sessionID, taskID: msg.TaskID, viewID: msg.ViewID, err: err}
	}
}
