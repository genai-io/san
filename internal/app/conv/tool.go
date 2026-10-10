package conv

import (
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
	// Progress holds the latest output counter of a running command ("1.2k
	// lines"), keyed by tool-call ID — the replaceable companion to the
	// elapsed timer, showing output is actually flowing. Cleared with StartedAt.
	Progress map[string]string
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
// show how long it has been running. Called on its PreToolEvent, and again
// when a permission prompt is approved — the first stamp then covers only how
// long the user took to answer, which is not what the row is reporting.
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

// SetProgress records the latest output counter for a running tool call. A blank
// text clears it (nothing to show yet).
func (t *ToolExecState) SetProgress(toolCallID, text string) {
	if text == "" {
		delete(t.Progress, toolCallID)
		return
	}
	if t.Progress == nil {
		t.Progress = make(map[string]string)
	}
	t.Progress[toolCallID] = text
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
