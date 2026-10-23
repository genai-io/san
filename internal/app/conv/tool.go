package conv

import (
	"strings"
	"time"

	"github.com/genai-io/san/internal/core"
)

// --- Tool state ---

type ToolExecState struct {
	PendingCalls []core.ToolCall
	CurrentIdx   int
	completed    map[string]bool
	// StartedAt stamps when each call began executing (its PreToolEvent),
	// keyed by tool-call ID. The view reads it to show a live elapsed timer
	// on a running row, so a long command no longer looks stuck. Only
	// in-flight calls appear; it is cleared with every new batch.
	StartedAt map[string]time.Time
	// Progress holds the latest progress of a running command, keyed by
	// tool-call ID: the companion to the elapsed timer and the row's preview.
	// Cleared with StartedAt.
	Progress map[string]ToolProgress
	// AwaitingApprovalID is the tool call parked on the permission modal. Its
	// PreToolEvent already stamped it as started, so without this the row would
	// spin and tick as if the call had been allowed to run. Empty while no
	// permission request is open.
	AwaitingApprovalID string
}

func (t *ToolExecState) Reset() {
	t.ClearPending()
	t.StartedAt = nil
	t.Progress = nil
	t.AwaitingApprovalID = ""
}

func (t *ToolExecState) Track(calls []core.ToolCall) {
	t.Reset()
	t.PendingCalls = append([]core.ToolCall(nil), calls...)
}

func (t *ToolExecState) MarkCurrent(toolCallID string) {
	for i, tc := range t.PendingCalls {
		if tc.ID == toolCallID {
			t.CurrentIdx = i
			return
		}
	}
}

// MarkStarted stamps the moment a tool call began executing, so the view can
// show how long it has been running. Called when its run begins, and on a
// subagent's approved prompt for the parent call, whose earlier stamp would
// otherwise count the user's deliberation.
func (t *ToolExecState) MarkStarted(toolCallID string) {
	if toolCallID == "" {
		return
	}
	if t.StartedAt == nil {
		t.StartedAt = make(map[string]time.Time)
	}
	t.StartedAt[toolCallID] = time.Now()
}

// MarkAwaitingApproval records the call whose permission request is open, so
// its row reports waiting rather than running. Only one request is on screen
// at a time; a later one replaces it.
func (t *ToolExecState) MarkAwaitingApproval(toolCallID string) {
	t.AwaitingApprovalID = toolCallID
}

// ClearAwaitingApproval drops the waiting state once the request is answered.
func (t *ToolExecState) ClearAwaitingApproval() {
	t.AwaitingApprovalID = ""
}

// ToolProgress is a running command's latest progress: its output counter
// ("1.2k lines"), shown beside the timer, and its output's tail, shown below.
type ToolProgress struct {
	Counter string
	Tail    string
}

// SetProgress records a running tool call's progress from its activity
// message: the counter, then on later lines the output's tail. A blank
// message clears it (nothing to show yet).
func (t *ToolExecState) SetProgress(toolCallID, message string) {
	if message == "" {
		delete(t.Progress, toolCallID)
		return
	}
	if t.Progress == nil {
		t.Progress = make(map[string]ToolProgress)
	}
	counter, tail, _ := strings.Cut(message, "\n")
	t.Progress[toolCallID] = ToolProgress{Counter: counter, Tail: tail}
}

func (t *ToolExecState) IndexOf(toolCallID string) int {
	for i, tc := range t.PendingCalls {
		if tc.ID == toolCallID {
			return i
		}
	}
	return -1
}

func (t *ToolExecState) MarkComplete(toolCallID string) bool {
	if t.IndexOf(toolCallID) < 0 || t.completed[toolCallID] {
		return false
	}
	if t.completed == nil {
		t.completed = make(map[string]bool)
	}
	t.completed[toolCallID] = true
	for i, tc := range t.PendingCalls {
		if !t.completed[tc.ID] {
			t.CurrentIdx = i
			return false
		}
	}
	t.ClearPending()
	return true
}

func (t *ToolExecState) ClearPending() {
	t.PendingCalls = nil
	t.CurrentIdx = 0
	t.completed = nil
}

// DrainPendingCalls returns unfinished calls regardless of completion order.
func (t *ToolExecState) DrainPendingCalls() []core.ToolCall {
	var calls []core.ToolCall
	for _, tc := range t.PendingCalls {
		if !t.completed[tc.ID] {
			calls = append(calls, tc)
		}
	}
	t.Reset()
	return calls
}
