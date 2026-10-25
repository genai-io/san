package input

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/task"
)

// TaskViewItem is a display-only snapshot. Output is recent bounded output, not
// a promise of a complete historical log. Status preserves the backend lifecycle.
type TaskViewItem struct {
	ID, AgentName, Description, Subject, Progress, Output, Error string
	OutputFile                                                   string
	Recorded                                                     bool // Persisted entry without a live worker.
	Status                                                       task.TaskStatus
	AwaitingApproval, ApprovalQueued, OutputTruncated            bool
	CanContinue                                                  bool
	Attempt                                                      int
	SessionID                                                    string
	Attempts                                                     string // Concise immutable run history, not the child conversation.
}
type TaskStopMsg struct {
	TaskID  string
	ViewID  uint64
	Attempt int
}
type TaskContinueMsg struct {
	TaskID  string
	ViewID  uint64
	Attempt int
}

type TaskReviewApprovalMsg struct {
	TaskID string
	ViewID uint64
}
type TaskViewerClosedMsg struct{ ViewID uint64 }

// TaskViewer never mutates tasks. The caller owns all lifecycle operations.
type TaskViewer struct {
	active, detail, confirm, confirmStop bool
	historyExpanded                      bool
	width, height                        int
	body                                 viewport.Model
	rowOffsets                           map[string]int
	items                                []TaskViewItem
	selected                             string
	archived                             map[string]TaskViewItem
	stopID                               string
	stopAttempt                          int
	viewID                               uint64
	pendingAction                        bool
}

func (v *TaskViewer) IsActive() bool      { return v.active }
func (v *TaskViewer) ViewID() uint64      { return v.viewID }
func (v *TaskViewer) ActionPending() bool { return v.pendingAction }
func (v *TaskViewer) Show(items []TaskViewItem, width, height int) {
	*v = TaskViewer{active: true, viewID: v.viewID + 1, body: viewport.New(), archived: make(map[string]TaskViewItem)}
	v.body.FillHeight = true
	v.Resize(width, height)
	v.Refresh(items)
}
func (v *TaskViewer) Resize(width, height int) {
	v.width, v.height = max(1, width), max(1, height)
	v.body.SetWidth(max(1, v.width-1))
	v.rebuild()
}
func (v *TaskViewer) current() (TaskViewItem, bool) {
	if item, ok := v.live(v.selected); ok {
		return item, true
	}
	item, ok := v.archived[v.selected]
	return item, ok
}
func (v *TaskViewer) live(id string) (TaskViewItem, bool) {
	for _, item := range v.items {
		if item.ID == id {
			return item, true
		}
	}
	return TaskViewItem{}, false
}
func (v *TaskViewer) Refresh(items []TaskViewItem) {
	if v.archived == nil {
		v.archived = make(map[string]TaskViewItem)
	}
	v.items = nil
	seen := make(map[string]bool)
	for _, item := range items {
		if item.ID != "" && !seen[item.ID] {
			v.items = append(v.items, item)
			seen[item.ID] = true
			v.archived[item.ID] = item
		}
	}
	if v.selected == "" && len(v.items) > 0 {
		v.selected = v.items[0].ID
	}
	if v.confirm {
		item, ok := v.live(v.stopID)
		if !ok || item.Status != task.StatusRunning || item.Attempt != v.stopAttempt {
			v.confirm = false
			v.confirmStop = false
			v.stopID = ""
		}
	}
	v.rebuild()
}
func taskViewStatus(item TaskViewItem) string {
	if item.Status == task.StatusRunning && item.AwaitingApproval {
		if item.ApprovalQueued {
			return "awaiting approval (queued request)"
		}
		return "awaiting approval (current request)"
	}
	if item.Status == "" {
		return "unknown"
	}
	return string(item.Status)
}
func taskViewSubject(item TaskViewItem) string {
	subject := item.Subject
	if subject == "" {
		subject = item.Description
	}
	owner := strings.TrimSpace(item.AgentName)
	if owner != "" && !strings.EqualFold(owner, "agent") && !strings.EqualFold(owner, "bash") && !strings.EqualFold(owner, subject) && !strings.HasPrefix(subject, owner+": ") {
		return owner + ": " + subject
	}
	return subject
}

func taskViewStopped(item TaskViewItem) bool {
	return item.Status == task.StatusStopped || item.Status == task.StatusKilled
}

func (v *TaskViewer) rebuild() {
	if !v.active || v.confirm {
		return
	}
	if !v.detail {
		v.selectVisible()
	}
	v.body.SetHeight(max(1, v.height-4-strings.Count(v.hint(), "\n")-1-transcriptFrameMargin))
	offset := v.body.YOffset()
	var lines []string
	if v.detail {
		item, ok := v.current()
		if !ok {
			lines = []string{"Task no longer available."}
		} else {
			lines = []string{taskViewSubject(item), "Owner: " + item.AgentName, "ID: " + item.ID, "Status: " + taskViewStatus(item)}
			if item.SessionID != "" {
				lines = append(lines, "Session: "+item.SessionID)
			}
			if item.Attempt > 0 {
				lines = append(lines, fmt.Sprintf("Attempt: %d", item.Attempt))
			}
			if item.Attempts != "" {
				lines = append(lines, "Run history:\n"+item.Attempts)
			}
			if _, live := v.live(item.ID); !live {
				lines = append(lines, "No longer in the live task list; showing last snapshot.")
			}
			if item.Recorded {
				lines = append(lines, "Recorded task; live worker unavailable.")
			}
			if item.OutputFile != "" {
				lines = append(lines, "Output log: "+item.OutputFile)
			}
			if item.Status == task.StatusRunning && item.Progress != "" {
				lines = append(lines, "Progress: "+item.Progress)
			}
			if item.Error != "" && (!taskViewStopped(item) || !kit.IsRoutineStopReason(item.Error)) {
				label := "Error: "
				if taskViewStopped(item) {
					label = "Reason: "
				}
				lines = append(lines, label+item.Error)
			}
			label := "Recent output"
			if item.OutputTruncated {
				label += " (truncated)"
			}
			output := item.Output
			if taskViewStopped(item) && kit.IsRoutineStopReason(strings.TrimPrefix(strings.TrimSpace(output), "Error: ")) {
				output = ""
			}
			if output == "" {
				output = "No recent output available."
			}
			lines = append(lines, "", label, output)
		}
	} else {
		active, history := v.groupedItems()
		v.rowOffsets = make(map[string]int, len(v.items))
		rowOffset := 0
		addLine := func(line string) {
			lines = append(lines, line)
			rowOffset += strings.Count(ansi.Wrap(line, max(1, v.width-2), " "), "\n") + 1
		}
		addItem := func(item TaskViewItem) {
			v.rowOffsets[item.ID] = rowOffset
			prefix := "  ● "
			if item.ID == v.selected {
				prefix = "› ● "
			}
			addLine(prefix + taskViewSubject(item) + " [" + taskViewStatus(item) + "]")
		}
		for _, item := range active {
			addItem(item)
		}
		if len(active) == 0 {
			if len(history) == 0 {
				addLine("No background tasks.")
			} else {
				addLine("No active background tasks.")
			}
		}
		if len(history) > 0 {
			addLine("")
			addLine(v.historySummary(history))
			if v.historyExpanded {
				for _, item := range history {
					addItem(item)
				}
			}
		}
		if v.selected != "" {
			if _, ok := v.live(v.selected); !ok {
				lines = append(lines, "Selected task disappeared. Select a row to continue.")
			}
		}
	}
	v.body.SetContent(ansi.Wrap(strings.Join(lines, "\n"), max(1, v.width-2), " "))
	v.body.SetYOffset(offset)
}
func (v *TaskViewer) close() { *v = TaskViewer{viewID: v.viewID} }

// Hide invalidates pending actions and releases the session's display snapshots.
func (v *TaskViewer) Hide() { *v = TaskViewer{viewID: v.viewID + 1} }

// Suspend acknowledges an action while preserving selection and detail scroll.
func (v *TaskViewer) Suspend() {
	v.active, v.confirm, v.confirmStop, v.pendingAction = false, false, false, false
	v.stopID = ""
}

// Resume returns from a root-owned action to the same task, not a row index.
func (v *TaskViewer) Resume(items []TaskViewItem, width, height int) {
	v.viewID++
	v.active, v.pendingAction = true, false
	v.Resize(width, height)
	v.Refresh(items)
}
func (v *TaskViewer) HandleKeypress(key tea.KeyMsg) tea.Cmd {
	if !v.active {
		return nil
	}
	k := key.String()
	// Repeated activation keys must never confirm a destructive action.
	if key.Key().IsRepeat && (k == "enter" || k == "s" || k == "r" || k == "c") {
		return nil
	}
	if v.confirm {
		switch k {
		case "esc", "q":
			v.confirm = false
			v.confirmStop = false
		case "left", "right", "tab":
			v.confirmStop = !v.confirmStop
		case "enter":
			id := v.stopID
			item, ok := v.live(id)
			if v.confirmStop && ok && !item.Recorded && item.Status == task.StatusRunning && v.selected == id && item.Attempt == v.stopAttempt {
				viewID, attempt := v.viewID, v.stopAttempt
				v.Suspend()
				v.pendingAction = true
				return func() tea.Msg { return TaskStopMsg{TaskID: id, ViewID: viewID, Attempt: attempt} }
			}
			v.confirm = false
			v.confirmStop = false
		}
		v.rebuild()
		return nil
	}
	switch k {
	case "esc", "q", "ctrl+c":
		if v.detail {
			v.detail = false
			v.body.GotoTop()
			v.rebuild()
		} else {
			viewID := v.viewID
			v.close()
			return func() tea.Msg { return TaskViewerClosedMsg{ViewID: viewID} }
		}
	case "enter":
		if _, ok := v.current(); ok {
			v.detail = true
			v.body.GotoTop()
			v.rebuild()
		}
	case "h":
		if !v.detail && !key.Key().IsRepeat {
			v.historyExpanded = !v.historyExpanded
			v.body.GotoTop()
			v.rebuild()
		}
	case "s":
		if item, ok := v.live(v.selected); ok && !item.Recorded && item.Status == task.StatusRunning {
			v.confirm = true
			v.confirmStop = false
			v.stopID, v.stopAttempt = item.ID, item.Attempt
			v.rebuild()
		}
	case "c":
		if item, ok := v.live(v.selected); ok && !item.Recorded && item.CanContinue && (item.Status == task.StatusFailed || item.Status == task.StatusCompleted) {
			id, viewID, attempt := item.ID, v.viewID, item.Attempt
			v.Suspend()
			v.pendingAction = true
			return func() tea.Msg { return TaskContinueMsg{TaskID: id, ViewID: viewID, Attempt: attempt} }
		}
	case "r":
		if item, ok := v.live(v.selected); ok && !item.Recorded && item.Status == task.StatusRunning && item.AwaitingApproval {
			id, viewID := item.ID, v.viewID
			v.Suspend()
			v.pendingAction = true
			return func() tea.Msg { return TaskReviewApprovalMsg{TaskID: id, ViewID: viewID} }
		}
	case "up", "k", "down", "j":
		if v.detail {
			if k == "up" || k == "k" {
				v.body.ScrollUp(1)
			} else {
				v.body.ScrollDown(1)
			}
			break
		}
		items := v.visibleItems()
		index := -1
		for i, item := range items {
			if item.ID == v.selected {
				index = i
			}
		}
		if k == "up" || k == "k" {
			index = max(0, index-1)
		} else {
			index = min(len(items)-1, index+1)
		}
		if index >= 0 && index < len(items) {
			v.selected = items[index].ID
			v.rebuild()
			row := v.rowOffsets[v.selected]
			if row < v.body.YOffset() {
				v.body.SetYOffset(row)
			} else if row >= v.body.YOffset()+v.body.Height() {
				v.body.SetYOffset(row - v.body.Height() + 1)
			}
		}
	case "pgup":
		v.body.PageUp()
	case "pgdown":
		v.body.PageDown()
	case "home":
		v.body.GotoTop()
	case "end":
		v.body.GotoBottom()
	}
	return nil
}
func (v *TaskViewer) hint() string {
	hint := "↑/↓ select · Enter details\ns stop · r approval · Esc close"
	if _, history := v.groupedItems(); !v.detail && len(history) > 0 {
		hint = "↑/↓ select · Enter · h history\ns stop · r approval · Esc close"
	}
	if v.detail {
		hint = "↑/↓ scroll · PgUp/PgDown\ns stop · r approval · Esc back"
	}
	if item, ok := v.live(v.selected); ok && !item.Recorded && item.CanContinue && (item.Status == task.StatusFailed || item.Status == task.StatusCompleted) {
		label := "c ask main to continue"
		if v.width < 36 {
			label = "c continue"
		}
		hint = strings.ReplaceAll(hint, "s stop · r approval", label)
	}
	if item, ok := v.current(); ok && item.Recorded {
		hint = strings.ReplaceAll(hint, "s stop · r approval · ", "")
	}
	if v.confirm {
		hint = "←/→ choose · Enter confirm\nEsc back"
	}
	return ansi.Wrap(hint, max(1, v.width-1), " ")
}

func (v *TaskViewer) Render() string {
	if !v.active {
		return ""
	}
	rule := kit.DimStyle().Render(strings.Repeat("─", max(1, v.width-1)))
	if v.confirm {
		choice := "[Back]   Stop task"
		if v.confirmStop {
			choice = "Back   [Stop task]"
		}
		return strings.Join([]string{rule, ansi.Wrap("Stop task "+v.stopID+"?", max(1, v.width-1), " "), "Ends this task; no resume.", "", choice, rule, kit.HintLine(v.hint())}, "\n")
	}
	active, _ := v.groupedItems()
	title := kit.TruncateText(fmt.Sprintf("Tasks · %d active", len(active)), max(1, v.width-1))
	return strings.Join([]string{rule, kit.SelectorTitleStyle().Render(title), "", v.body.View(), rule, kit.HintLine(v.hint())}, "\n")
}
