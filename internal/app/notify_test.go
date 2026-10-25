package app

import (
	"encoding/xml"
	"strings"
	"testing"

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
		if strings.TrimSpace(view) != "◆ Security review stopped" {
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
				if !strings.Contains(view, string(status)) || strings.Contains(view, "nested diagnostic") {
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
	if !strings.Contains(view, description+" completed") {
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
