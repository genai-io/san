package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/todo"
)

func reviewerCalls(n int) []core.ToolCall {
	var calls []core.ToolCall
	for i := range n {
		name := fmt.Sprintf("WORKER_%d", i)
		calls = append(calls, core.ToolCall{ID: name, Name: "Agent", Input: fmt.Sprintf(`{"name":%q,"description":"Review package","mode":"explore"}`, name)})
	}
	return calls
}

func setReviewerActivity(m *model, calls []core.ToolCall) {
	m.conv.Tool.Track(calls)
	m.conv.TaskActivity = make(map[int][]string)
	for i, call := range calls {
		m.conv.Tool.MarkStarted(call.ID)
		m.conv.TaskActivity[i] = []string{"Model: worker-model", "Mode: Explore", "Read(a.go)", "Read(b.go)", "Read(c.go)", "Read(d.go)", "Read(e.go)", "Read(f.go)"}
	}
	m.conv.Stream.Active = true
}

func TestFourWorkerIdentitiesSurviveExpandAndShortTerminal(t *testing.T) {
	for _, height := range []int{24, 10, 7} {
		m := fixedComposerModel(80, height)
		calls := reviewerCalls(4)
		m.conv.Messages = []core.ChatMessage{{Role: core.ChatAssistant, ToolCalls: calls}}
		setReviewerActivity(m, calls)
		for range 2 {
			frame, cursor := m.viewString()
			plain := ansi.Strip(frame)
			if rowCount(frame) != height || cursor.Position.Y != height-3 {
				t.Fatalf("height=%d displaced composer: cursor=%+v\n%s", height, cursor, plain)
			}
			for _, call := range calls {
				if !strings.Contains(plain, call.ID) && !strings.Contains(plain, "active workers omitted") {
					t.Fatalf("height=%d hid %s without summary:\n%s", height, call.ID, plain)
				}
			}
			if height >= 10 && strings.Contains(plain, "active workers omitted") {
				t.Fatalf("four identities should fit at height %d:\n%s", height, plain)
			}
			m.conv.ToggleAllExpandable()
		}
	}
}

func TestWorkerIdentityLayoutReservesDockedModal(t *testing.T) {
	calls := reviewerCalls(4)
	m := dockedModalModelWithCalls(t, strings.Repeat("Review rationale ", 80), calls)
	setReviewerActivity(m, calls)
	for range 2 {
		frame, _ := m.viewString()
		plain := ansi.Strip(frame)
		if rowCount(frame) != m.env.Height || !strings.Contains(plain, "Do you want to proceed?") {
			t.Fatalf("workers displaced modal:\n%s", plain)
		}
		for _, call := range calls {
			if !strings.Contains(plain, call.ID) && !strings.Contains(plain, "active workers omitted") {
				t.Fatalf("modal hid %s without summary:\n%s", call.ID, plain)
			}
		}
		m.conv.ToggleAllExpandable()
	}
}

func TestWorkerOverflowKeepsLatestErrorNotice(t *testing.T) {
	m := fixedComposerModel(80, 10)
	calls := reviewerCalls(4)
	m.conv.Messages = []core.ChatMessage{
		{Role: core.ChatAssistant, ToolCalls: calls, ToolCallsExpanded: true},
		{Role: core.ChatNotice, Content: "WORKER_ERROR: permission denied", AgentNotice: true},
	}
	setReviewerActivity(m, calls)
	frame, _ := m.viewString()
	plain := ansi.Strip(frame)
	if !strings.Contains(plain, "WORKER_ERROR") || rowCount(frame) != 10 {
		t.Fatalf("worker layout hid a live error or displaced footer:\n%s", plain)
	}
	for _, call := range calls {
		if !strings.Contains(plain, call.ID) {
			t.Fatalf("worker disappeared to make room for notice: %s\n%s", call.ID, plain)
		}
	}
}

func TestBackgroundWorkerProgressIsLiveAndDisplayOnly(t *testing.T) {
	task.ResetDefaultTracker()
	t.Cleanup(task.ResetDefaultTracker)
	m := fixedComposerModel(80, 24)
	m.services.Task = task.Default()
	worker := task.NewAgentTask("background-worker", "reviewer", "Review source", context.Background(), nil, "")
	m.services.Task.RegisterTask(worker)
	todo.TrackWorker(m.services.Tracker, worker.GetStatus())
	worker.AppendProgress("Read(LIVE_ACTIVITY_MARKER.go)")
	worker.AppendProgress("Usage: input=10 output=2")
	view := ansi.Strip(m.renderTrackerList())
	if !strings.Contains(view, "LIVE_ACTIVITY_MARKER") || strings.Contains(view, "stalled") {
		t.Fatalf("background progress hidden or falsely stalled: %s", view)
	}
	if worker.GetOutput() != "" {
		t.Fatal("display activity leaked into result")
	}
}

func TestFourBackgroundWorkerRowsStayVisible(t *testing.T) {
	task.ResetDefaultTracker()
	t.Cleanup(task.ResetDefaultTracker)
	m := fixedComposerModel(80, 10)
	m.services.Task = task.Default()
	for i := range 4 {
		name := fmt.Sprintf("REVIEWER_%d", i)
		worker := task.NewAgentTask(name, name, "Review source", context.Background(), nil, "")
		m.services.Task.RegisterTask(worker)
		todo.TrackWorker(m.services.Tracker, worker.GetStatus())
		worker.AppendProgress("Read(source.go)")
	}
	frame, _ := m.viewString()
	plain := ansi.Strip(frame)
	for i := range 4 {
		if !strings.Contains(plain, fmt.Sprintf("REVIEWER_%d", i)) {
			t.Fatalf("background activity displaced sibling identity:\n%s", plain)
		}
	}
	if rowCount(frame) != 10 {
		t.Fatalf("background activity displaced footer:\n%s", plain)
	}
}

func TestFailedWorkerLogFailureStillPreservesPartialOutput(t *testing.T) {
	for _, status := range []task.TaskStatus{task.StatusFailed, task.StatusStopped} {
		for _, output := range []string{"PARTIAL_FINDINGS <inspected>", "PARTIAL_FINDINGS " + strings.Repeat("审查报告", 2000)} {
			message, ok := taskCompletionMessage(task.TaskInfo{
				ID: "review", Type: task.TaskTypeAgent, Status: status,
				Error: "provider <unavailable>", Output: output,
				OutputFile: "/unusable/log", OutputError: "disk <failure>",
			})
			for _, want := range []string{"Error: provider &lt;unavailable&gt;", "Partial output:", "PARTIAL_FINDINGS", "disk &lt;failure&gt;"} {
				if !ok || !strings.Contains(message.Content, want) {
					t.Fatalf("status=%s log failure hid error/partial output: missing %q", status, want)
				}
			}
			if strings.Contains(message.Content, "output-file=") || !utf8.ValidString(message.Content) {
				t.Fatal("failed worker advertised an unusable log or split a UTF-8 character")
			}
			if len(output) > maxTaskOutputInNotification && !strings.Contains(message.Content, "retained bytes omitted") {
				t.Fatal("large partial output lost its explicit preview bound")
			}
		}
	}
}

func TestOutputLogFailureNoticePreservesOutcomeAndBoundsPreview(t *testing.T) {
	for _, output := range []string{"REPORT_MARKER findings", strings.Repeat("审查报告", 2000)} {
		message, ok := taskCompletionMessage(task.TaskInfo{ID: "review", Type: task.TaskTypeAgent, Status: task.StatusCompleted, Output: output, OutputFile: "/unusable/log", OutputError: "disk failure"})
		if !ok || !strings.Contains(message.Subject, "completed") || !strings.Contains(message.Subject, "output log unavailable") || strings.Contains(message.Content, "output-file=") || !strings.Contains(message.Content, "disk failure") {
			t.Fatalf("unusable log advertised or success changed: %+v", message)
		}
		if !utf8.ValidString(message.Content) {
			t.Fatal("preview split a UTF-8 character")
		}
		if len(output) > maxTaskOutputInNotification {
			if !strings.Contains(message.Content, "retained bytes omitted") || len(message.Content) > maxTaskOutputInNotification+500 {
				t.Fatalf("degraded preview missing bound/omission: %d bytes", len(message.Content))
			}
		} else if !strings.Contains(message.Content, output) {
			t.Fatal("short result lost on log failure")
		}
	}
}
