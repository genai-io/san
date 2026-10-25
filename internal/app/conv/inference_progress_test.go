package conv

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/core"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func inferenceView(m *Model, width int) string {
	return stripANSI(RenderActiveContent(RenderContext{
		Messages: m.Messages, CommittedCount: m.CommittedCount,
		StreamActive: m.Stream.Active, StreamMessageID: m.Stream.MessageID,
		StreamStartedAt: m.Stream.StartedAt, Inferring: m.Stream.Inferring,
		InferenceAttempt: m.Stream.Attempt, GeneratingTool: m.Stream.GeneratingTool,
		GeneratedBytes: m.Stream.GeneratedBytes, Width: width,
	}))
}

func TestInferenceProgressRemainsVisibleAfterTextAndNotices(t *testing.T) {
	m, rt := NewModel(80), &streamRuntime{}
	applyAgentEvent(rt, &m, sdkagent.InferenceStart{Attempt: 1})
	m.Stream.StartedAt = time.Now().Add(-112 * time.Second)
	if view := inferenceView(&m, 80); strings.Count(view, "✦") != 1 || !strings.Contains(view, "Generating response") {
		t.Fatalf("the first wait needs one generation indicator: %q", view)
	}
	m.AppendStream("I will write the HTML now.", "")
	m.AddNotice("a background task completed")
	applyChunk(rt, &m, ai.Event{Type: ai.EventProgress, Progress: &ai.Progress{
		ToolCallID: "write-1", ToolName: "Write", InputBytes: 33051,
	}})
	view := inferenceView(&m, 80)
	for _, want := range []string{"I will write the HTML now.", "a background task completed", "Generating Write arguments", "33051 bytes", "1m 52s"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q in %q", want, view)
		}
	}
	if len(m.Tool.PendingCalls) != 0 || len(m.Messages[0].ToolCalls) != 0 || m.Stream.BuildingTool != "" {
		t.Fatal("generation progress installed or started an unfinished tool")
	}
	if m.Messages[0].Content != "I will write the HTML now." {
		t.Fatal("generation progress leaked into assistant content")
	}
	// A fully flushed commentary block still needs a live status, independent
	// of its immutable scrollback or the last notice's identity.
	m.Messages[0].ContentCommittedLen = len(m.Messages[0].Content)
	m.Messages[0].BulletEmitted = true
	if view := inferenceView(&m, 80); !strings.Contains(view, "Generating Write arguments") {
		t.Fatalf("committing commentary hid the live generation: %q", view)
	}
}

func TestInferenceProgressClearsAtFailureRetryAndCancellation(t *testing.T) {
	m, rt := NewModel(80), &streamRuntime{}
	progress := ai.Event{Type: ai.EventProgress, Progress: &ai.Progress{ToolName: "Write", InputBytes: 1024}}
	applyAgentEvent(rt, &m, sdkagent.InferenceStart{Attempt: 1})
	applyChunk(rt, &m, progress)
	owner := m.Stream.MessageID
	applyAgentEvent(rt, &m, sdkagent.InferenceEnd{Attempt: 1, Err: errors.New("stream stalled")})
	if m.Stream.Inferring || m.Stream.GeneratingTool != "" || m.Stream.GeneratedBytes != 0 || m.Stream.MessageID != owner {
		t.Fatalf("failure did not clear generation while preserving the retry owner: %+v", m.Stream)
	}
	applyChunk(rt, &m, progress)
	if m.Stream.GeneratedBytes != 0 {
		t.Fatal("late progress revived a failed inference")
	}
	applyAgentEvent(rt, &m, sdkagent.InferenceStart{Attempt: 2})
	if view := inferenceView(&m, 100); !strings.Contains(view, "Retrying · attempt 2") || strings.Contains(view, "1024 bytes") {
		t.Fatalf("retry lost its state or inherited old progress: %q", view)
	}
	m.Stream.Stop()
	applyChunk(rt, &m, progress)
	if m.Stream.Inferring || m.Stream.GeneratedBytes != 0 || strings.Contains(inferenceView(&m, 100), "Generating response") {
		t.Fatal("late progress revived a cancelled inference")
	}
}

func TestInferenceProgressStopsBeforeToolExecution(t *testing.T) {
	m, rt := NewModel(80), &streamRuntime{}
	applyAgentEvent(rt, &m, sdkagent.InferenceStart{Attempt: 1})
	applyChunk(rt, &m, ai.Event{Type: ai.EventProgress, Progress: &ai.Progress{ToolName: "Write", InputBytes: 100}})
	applyAgentEvent(rt, &m, sdkagent.InferenceEnd{Response: &ai.Response{Content: ai.Content{
		ai.ToolCallBlock(ai.ToolCall{ID: "write-1", Name: "Write", Input: `{}`}),
	}}})
	if m.Stream.Inferring || len(m.Tool.PendingCalls) != 1 || m.Stream.GeneratedBytes != 0 {
		t.Fatalf("completed inference did not transition to pending execution: %+v", m.Stream)
	}
	applyAgentEvent(rt, &m, sdkagent.ToolUpdate{ID: "write-1", Name: "Write"})
	if m.Stream.BuildingTool != "Write" || m.Stream.Inferring {
		t.Fatal("tool execution was mistaken for argument generation")
	}
}

func TestInferenceStatusIsBoundedAndFreezesUnderModal(t *testing.T) {
	p := RenderContext{StreamActive: true, Inferring: true, InferenceAttempt: 2,
		GeneratingTool: strings.Repeat("Write", 20), GeneratedBytes: 33051,
		StreamStartedAt: time.Now().Add(-112 * time.Second)}
	for _, width := range []int{1, 20, 40, 80} {
		p.Width = width
		if row := RenderInferenceStatus(p); ansi.StringWidth(row) > width || strings.Contains(row, "\n") {
			t.Fatalf("generation row exceeds width %d: %q", width, row)
		}
	}
	p.Width, p.DockedModalActive = 80, true
	if row := stripANSI(RenderInferenceStatus(p)); !strings.Contains(row, "Waiting for your response") || strings.Contains(row, "1m") {
		t.Fatalf("a modal must not time the user's answer as generation: %q", row)
	}
}

func TestFailedTurnNoticeMarksPartialOutputIncomplete(t *testing.T) {
	notice := FailedTurnNotice(errors.New("stream stalled"))
	if view := stripANSI(RenderSystemMessage(notice)); !strings.Contains(view, "Turn failed") || !strings.Contains(view, "output is incomplete") || !strings.Contains(view, "stream stalled") {
		t.Fatalf("failed turn resembles normal completion: %q", view)
	}
	m := NewModel(80)
	m.Append(core.ChatMessage{Role: core.ChatNotice, Content: notice})
	if view := inferenceView(&m, 80); !strings.Contains(view, "output is incomplete") {
		t.Fatal("failure notice did not survive ordinary transcript rendering")
	}
}
