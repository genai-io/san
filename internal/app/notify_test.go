package app

import (
	"strings"
	"testing"

	"github.com/genai-io/san/internal/broker"
	"github.com/genai-io/san/internal/task"
)

// A broker message routed to main is an agent message, so its notice is marked
// FromAgent and merging preserves that whenever any component came from an agent.
func TestNoticeFromAgentPropagates(t *testing.T) {
	if n := fromBrokerMessage(broker.Message{Subject: "Backend: research completed"}); !n.FromAgent {
		t.Fatal("fromBrokerMessage should mark the notice as FromAgent")
	}
	merged := mergeNotices([]mainNotice{
		{Display: "system note"},
		{Display: "Backend done", FromAgent: true},
	})
	if !merged.FromAgent {
		t.Fatalf("merged notice should be FromAgent when any component is, got %+v", merged)
	}
	plain := mergeNotices([]mainNotice{{Display: "a"}, {Display: "b"}})
	if plain.FromAgent {
		t.Fatalf("merged notice should not be FromAgent when no component is, got %+v", plain)
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
