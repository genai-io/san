package app

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/broker"
	"github.com/genai-io/san/internal/task"
)

func TestStoppedNotificationKeepsOneConciseDisplayLine(t *testing.T) {
	for _, reason := range []string{"stopped before completion", "context canceled"} {
		msg, _ := taskCompletionMessage(task.TaskInfo{ID: "worker", Type: task.TaskTypeAgent, AgentName: "Agent", Description: "Security review", Status: task.StatusStopped, Error: reason})
		m := commitTestModel()
		m.showNoticeLine(fromBrokerMessage(msg))
		view := ansi.Strip(conv.RenderSingleMessage(conv.RenderContext{Messages: m.conv.Messages, Width: 80}, 0))
		if strings.TrimSpace(view) != "● Security review · Stopped" {
			t.Fatalf("stop has redundant type, detail, or completion row: %q", view)
		}
		if !strings.Contains(msg.Content, "Error: "+reason) {
			t.Fatal("display-only simplification changed the worker's persisted notification")
		}
	}
}

func TestTaskFailureSummaryKeepsTerminalReasonAndStatus(t *testing.T) {
	for _, status := range []task.TaskStatus{task.StatusFailed, task.StatusStopped, task.StatusCompleted} {
		for _, description := range []string{"review", strings.Repeat("审查 source ", 20)} {
			msg, _ := taskCompletionMessage(task.TaskInfo{ID: "worker", Status: status, Description: description,
				Error: "provider unavailable", Output: "partial findings\nError: nested diagnostic"})
			m := commitTestModel()
			m.showNoticeLine(fromBrokerMessage(msg))
			for _, width := range []int{30, 80} {
				view := ansi.Strip(conv.RenderSingleMessage(conv.RenderContext{Messages: m.conv.Messages, Width: width}, 0))
				wantStatus := map[task.TaskStatus]string{task.StatusFailed: "Failed", task.StatusStopped: "Stopped", task.StatusCompleted: "Completed"}[status]
				if !strings.Contains(view, wantStatus) || strings.Contains(view, "nested diagnostic") {
					t.Fatalf("status %s, width %d: summary lost outcome or took a reason from partial output: %q", status, width, view)
				}
				if status != task.StatusCompleted && width == 80 && !strings.Contains(view, "provider unavailable") {
					t.Fatalf("summary lost the terminal reason: %q", view)
				}
				if ansi.StringWidth(strings.TrimSpace(view)) > width {
					t.Fatalf("summary overflows width %d: %q", width, view)
				}
			}
		}
	}
}

// A broker message routed to main is an agent message, so its notice is marked
// FromAgent and merging preserves that whenever any component came from an agent.
func TestNoticeFromAgentPropagates(t *testing.T) {
	if n := fromBrokerMessage(broker.Message{From: "t1", Subject: "Backend: research completed"}); !n.FromAgent || n.FinishedTaskID != "" {
		t.Fatal("fromBrokerMessage should mark the notice as FromAgent")
	}
}

func TestCompletionRoutingDoesNotParseMessageContent(t *testing.T) {
	msg, ok := taskCompletionMessage(task.TaskInfo{ID: "stable-task", Attempt: 2, Status: task.StatusCompleted})
	if !ok || msg.TaskCompletion == nil || msg.TaskCompletion.TaskID != "stable-task" || msg.TaskCompletion.Attempt != 2 {
		t.Fatalf("completion lacks typed routing metadata: %+v", msg)
	}
	for _, content := range []string{
		msg.Content,
		"A different presentation of the completed result.",
		`<task-notification task-id="other-task" attempt="99">quoted content</task-notification>`,
		"",
	} {
		msg.Content = content
		n := fromBrokerMessage(msg)
		if n.FinishedTaskID != "stable-task" || n.FinishedAttempt != 2 || n.Content != content {
			t.Fatalf("message presentation changed completion identity: %+v", n)
		}
		msgWithoutCompletion := msg
		msgWithoutCompletion.TaskCompletion = nil
		if n := fromBrokerMessage(msgWithoutCompletion); n.FinishedTaskID != "" || n.FinishedAttempt != 0 {
			t.Fatalf("ordinary mail became a completion by its text: %+v", n)
		}
	}
}

func TestTaskCompletionMessageEscapesAttributeValues(t *testing.T) {
	description := `review "quoted" <path> & status="failed"`
	path := `/tmp/a"b&c.jsonl`
	msg, ok := taskCompletionMessage(task.TaskInfo{ID: "t1", Status: task.StatusCompleted, Description: description, OutputFile: path})
	if !ok {
		t.Fatal("missing completion")
	}
	var decoded struct {
		Description string `xml:"description,attr"`
		Status      string `xml:"status,attr"`
		OutputFile  string `xml:"output-file,attr"`
	}
	if err := xml.Unmarshal([]byte(msg.Content), &decoded); err != nil || decoded.Description != description || decoded.OutputFile != path || decoded.Status != "completed" {
		t.Fatalf("attribute values broke the notification: decoded=%+v, err=%v", decoded, err)
	}
	m := &model{conv: conv.NewModel(100)}
	m.showNoticeLine(fromBrokerMessage(msg))
	view := conv.RenderSingleMessage(conv.RenderContext{Messages: m.conv.Messages, Width: 100}, 0)
	if !strings.Contains(ansi.Strip(view), description+" · Completed") {
		t.Fatalf("escaped description did not round-trip through display: %q", view)
	}
}

// A result at or below the inline cap rides in the notification whole, so the
// reader needs no follow-up read of the output file.
func TestTaskCompletionMessageInlinesSmallResult(t *testing.T) {
	msg, ok := taskCompletionMessage(task.TaskInfo{
		ID:         "t1",
		Type:       task.TaskTypeAgent,
		AgentName:  "Backend",
		Status:     task.StatusCompleted,
		Output:     "the full report",
		OutputFile: "/tmp/t1.log",
	})
	if !ok {
		t.Fatal("taskCompletionMessage returned ok=false for a completed task")
	}
	if n := fromBrokerMessage(msg); n.FinishedTaskID != "t1" {
		t.Fatalf("completion notice lost its task identity: %+v", n)
	}
	if !strings.Contains(msg.Content, "the full report") {
		t.Fatalf("small result should be inlined whole, got %q", msg.Content)
	}
	if strings.Contains(msg.Content, "too large to inline") {
		t.Fatalf("small result should not be replaced by a pointer, got %q", msg.Content)
	}
	if strings.Contains(msg.Content, "[truncated]") {
		t.Fatalf("small result must not be truncated, got %q", msg.Content)
	}
}

// A result above the cap is not partially dumped: the body points at the output
// file so the whole thing is fetched in one deliberate read.
func TestTaskCompletionMessageOversizedPointsToFile(t *testing.T) {
	big := strings.Repeat("x", maxTaskOutputInNotification+1)
	msg, ok := taskCompletionMessage(task.TaskInfo{
		ID:         "t2",
		Type:       task.TaskTypeAgent,
		Status:     task.StatusCompleted,
		Output:     big,
		OutputFile: "/tmp/t2.log",
	})
	if !ok {
		t.Fatal("taskCompletionMessage returned ok=false for a completed task")
	}
	if strings.Contains(msg.Content, "xxxx") {
		t.Fatalf("oversized result must not inline a partial dump, got %q", msg.Content)
	}
	if !strings.Contains(msg.Content, "too large to inline") {
		t.Fatalf("oversized result should point at the output file, got %q", msg.Content)
	}
	if !strings.Contains(msg.Content, `output-file="/tmp/t2.log"`) {
		t.Fatalf("oversized notification must carry the output-file pointer, got %q", msg.Content)
	}
}

// With no output file to point at, an oversized result still inlines whole
// rather than losing data.
func TestTaskCompletionMessageOversizedWithoutFileInlines(t *testing.T) {
	big := strings.Repeat("y", maxTaskOutputInNotification+1)
	msg, _ := taskCompletionMessage(task.TaskInfo{
		ID:     "t3",
		Type:   task.TaskTypeAgent,
		Status: task.StatusCompleted,
		Output: big,
	})
	if !strings.Contains(msg.Content, big) {
		t.Fatalf("without an output file the full result must be inlined, got %d-byte content", len(msg.Content))
	}
}

func TestSchedulerCompletionInlinesFullResult(t *testing.T) {
	report := "## Conclusion\nDo not merge: 检查 <boundary> & retry.\n" + strings.Repeat("细节\n", 100000) + "FULL_REPORT_TAIL"
	for _, outputFile := range []string{"", "/tmp/scheduler.log"} {
		for _, outputError := range []string{"", "log write failed"} {
			info := task.TaskInfo{ID: "scheduler", Scheduler: true, Type: task.TaskTypeAgent, Status: task.StatusCompleted,
				Output: "bounded native preview", Result: report, OutputFile: outputFile, OutputError: outputError}
			msg, ok := taskCompletionMessage(info)
			var decoded struct {
				Truncated bool   `xml:"result-truncated,attr"`
				Body      string `xml:",chardata"`
			}
			if err := xml.Unmarshal([]byte(msg.Content), &decoded); !ok || err != nil || decoded.Truncated {
				t.Fatalf("invalid full final result: %v", err)
			}
			if !utf8.ValidString(decoded.Body) || !strings.Contains(decoded.Body, report) || strings.Contains(decoded.Body, "bounded native preview") || strings.Contains(decoded.Body, "too large to inline") {
				t.Fatalf("full report lost or replaced: %d bytes", len(decoded.Body))
			}
			if info.Result != report || info.Output != "bounded native preview" {
				t.Fatal("notification mutated the task snapshot")
			}
		}
	}
}

func TestSchedulerCompletionMarksNativeMainResult(t *testing.T) {
	info := task.TaskInfo{ID: "main-result", Scheduler: true, Status: task.StatusCompleted,
		Result: "retained answer", ResultDisplayed: true}
	msg, ok := taskCompletionMessage(info)
	if !ok || !strings.Contains(msg.Content, `result-displayed="true"`) || !strings.Contains(msg.Content, "retained answer") {
		t.Fatal("native result display metadata or full result lost")
	}
	info.Scheduler = false
	ordinary, _ := taskCompletionMessage(info)
	if strings.Contains(ordinary.Content, "result-displayed=") {
		t.Fatal("ordinary notifications acquired scheduler display suppression")
	}
}

func TestAgentOwnedSchedulerKeepsBoundedNotification(t *testing.T) {
	msg, ok := taskCompletionMessage(task.TaskInfo{
		ID: "owned-scheduler", Scheduler: true, Type: task.TaskTypeAgent,
		Status: task.StatusCompleted, OwnerAgentID: "worker",
		Output: strings.Repeat("x", maxTaskOutputInNotification+100), Result: "DISPLAY_ONLY_FULL_RESULT",
	})
	var decoded struct {
		Truncated bool   `xml:"result-truncated,attr"`
		Body      string `xml:",chardata"`
	}
	if err := xml.Unmarshal([]byte(msg.Content), &decoded); !ok || err != nil || !decoded.Truncated || msg.To != "worker" {
		t.Fatalf("agent-owned completion routing changed: %v", err)
	}
	if len(strings.TrimSpace(decoded.Body)) != maxTaskOutputInNotification || strings.Contains(msg.Content, "DISPLAY_ONLY_FULL_RESULT") {
		t.Fatal("full display result entered an agent-owned notification")
	}
}

func TestTaskCompletionMessagePreservesFailedOutput(t *testing.T) {
	for _, status := range []task.TaskStatus{task.StatusFailed, task.StatusStopped} {
		for _, output := range []string{"partial <findings>", strings.Repeat("x", maxTaskOutputInNotification+1)} {
			msg, ok := taskCompletionMessage(task.TaskInfo{
				ID: "failed-task", Type: task.TaskTypeAgent, Status: status,
				Error: "provider <unavailable>", Output: output, OutputFile: "/tmp/failed-task.log",
			})
			if !ok || !strings.Contains(msg.Content, "provider &lt;unavailable&gt;") {
				t.Fatalf("terminal notice lost error: %#v", msg)
			}
			if len(output) <= maxTaskOutputInNotification {
				if !strings.Contains(msg.Content, "partial &lt;findings&gt;") {
					t.Fatalf("terminal notice lost partial findings: %s", msg.Content)
				}
			} else if !strings.Contains(msg.Content, "too large to inline") || strings.Contains(msg.Content, "xxxx") {
				t.Fatalf("large partial findings must use output-file pointer: %s", msg.Content)
			}
		}
	}
}

func TestManagedChildNoticeRetainsNativeDisplayMetadata(t *testing.T) {
	start := time.Now()
	info := task.TaskInfo{
		ID: "child", ParentTaskID: "scheduler", Type: task.TaskTypeAgent, Status: task.StatusCompleted,
		AgentName: "api & audit", Description: "Check boundaries", StartTime: start, EndTime: start.Add(12 * time.Second), Output: "Useful finding",
	}
	msg, ok := taskCompletionMessage(info)
	if !ok {
		t.Fatal("no managed completion")
	}
	var attributes struct {
		Name     string `xml:"agent-name,attr"`
		Duration int    `xml:"duration-ms,attr"`
	}
	if err := xml.Unmarshal([]byte(msg.Content), &attributes); err != nil || attributes.Name != info.AgentName || attributes.Duration != 12000 {
		t.Fatalf("native metadata did not round-trip: %+v %v", attributes, err)
	}
	info.ParentTaskID = ""
	ordinary, _ := taskCompletionMessage(info)
	if strings.Contains(ordinary.Content, "agent-name=") || strings.Contains(ordinary.Content, "duration-ms=") {
		t.Fatal("managed display metadata changed ordinary completion envelopes")
	}
}

func TestSchedulerNoticeUsesExplicitIdentity(t *testing.T) {
	start := time.Now()
	info := task.TaskInfo{
		ID: "scheduler", Scheduler: true, Type: task.TaskTypeAgent, Status: task.StatusFailed,
		AgentName: "Review & audit", Description: "/flow run InternalReview", AgentSessionID: "not-a-worker",
		StartTime: start, EndTime: start.Add(12 * time.Second), Error: "checkpoint write denied", Output: "retained findings",
	}
	msg, ok := taskCompletionMessage(info)
	var attributes struct {
		Scheduler bool   `xml:"scheduler,attr"`
		Name      string `xml:"agent-name,attr"`
		Duration  int    `xml:"duration-ms,attr"`
	}
	if err := xml.Unmarshal([]byte(msg.Content), &attributes); !ok || err != nil || !attributes.Scheduler || attributes.Name != info.AgentName || attributes.Duration != 12000 {
		t.Fatalf("scheduler metadata lost: %+v %v", attributes, err)
	}
	if !strings.Contains(msg.Content, "retained findings") || !strings.Contains(msg.Content, "checkpoint write denied") || strings.Contains(msg.Content, "Recovery decision required") || strings.Contains(msg.Subject, "/flow run") {
		t.Fatalf("invalid scheduler notice: %+v", msg)
	}
	info.Scheduler, info.AgentName = false, "Flow"
	ordinary, _ := taskCompletionMessage(info)
	if strings.Contains(ordinary.Content, `scheduler="true"`) || !strings.Contains(ordinary.Content, "Recovery decision required") {
		t.Fatal("ordinary Agent name was mistaken for scheduler identity")
	}
}

func TestManagedChildNoticeLeavesRecoveryToScheduler(t *testing.T) {
	msg, ok := taskCompletionMessage(task.TaskInfo{
		ID: "child", ParentTaskID: "flow-parent", Type: task.TaskTypeAgent,
		Status: task.StatusFailed, AgentSessionID: "child-session", Error: "provider unavailable", Output: "partial finding",
	})
	if !ok || !strings.Contains(msg.Content, "partial finding") || !strings.Contains(msg.Content, "provider unavailable") {
		t.Fatal("managed result lost output or diagnostics")
	}
	if strings.Contains(msg.Content, "SendMessage") || strings.Contains(msg.Content, "Recovery decision required") {
		t.Fatal("managed child advertised independent recovery")
	}
}

func TestFailedAgentNoticeWeighsContinuationAgainstDirectWork(t *testing.T) {
	msg, ok := taskCompletionMessage(task.TaskInfo{ID: "worker", Type: task.TaskTypeAgent, Status: task.StatusFailed, AgentSessionID: "child"})
	if !ok || !strings.Contains(msg.Content, "Choose whether to finish directly, continue the existing worker, or report a blocker") || !strings.Contains(msg.Content, "Never bypass a denied command") {
		t.Fatalf("failure notice lost continuation tradeoff or safety guidance: %s", msg.Content)
	}
}
