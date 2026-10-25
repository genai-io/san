package app

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func modelWithInferenceProgress(t *testing.T, height int) *model {
	t.Helper()
	m := fixedComposerModel(80, height)
	m.services.Agent = &agent.Runtime{}
	m.services.Tool = tool.NewRegistry()
	m.env.SessionPermissions = setting.NewSessionPermissions()
	m.pendingDecisions = &sync.Map{}
	conv.Update(m, &m.conv, conv.AgentOutboxMsg{Event: sdkagent.InferenceStart{Attempt: 1}})
	conv.Update(m, &m.conv, conv.AgentOutboxMsg{Event: sdkagent.InferenceUpdate{Delta: ai.Event{
		Type: ai.EventBlockDelta, Block: ai.TextBlock("I will write the HTML now."),
	}}})
	conv.Update(m, &m.conv, conv.AgentOutboxMsg{Event: sdkagent.InferenceUpdate{Delta: ai.Event{
		Type: ai.EventProgress, Progress: &ai.Progress{ToolCallID: "write-1", ToolName: "Write", InputBytes: 33051},
	}}})
	return m
}

func TestInferenceProgressKeepsItsRowInShortViewsAndHistoryHandoffs(t *testing.T) {
	for _, height := range []int{5, 6, 8, 24} {
		for _, handoff := range []bool{false, true} {
			m := modelWithInferenceProgress(t, height)
			m.conv.AppendStream(strings.Repeat("\nlong commentary", 30), "")
			m.conv.AddNotice("background completed")
			if handoff {
				m.flush.pendingPrints = []pendingScrollbackPrint{{remaining: strings.Repeat("older history\n", 30)}}
			}
			view := m.View()
			frame := ansi.Strip(view.Content)
			if strings.Count(frame, "Generating Write arguments") != 1 || !strings.Contains(frame, "33051 bytes") {
				t.Fatalf("height=%d handoff=%v hid or duplicated generation status:\n%s", height, handoff, frame)
			}
			if rowCount(frame) != height || view.Cursor == nil || view.Cursor.Position.Y != height-3 {
				t.Fatalf("height=%d handoff=%v displaced the composer: rows=%d cursor=%+v", height, handoff, rowCount(frame), view.Cursor)
			}
		}
	}
}

func TestInferenceProgressCannotReviveAfterUserCancellation(t *testing.T) {
	m := modelWithInferenceProgress(t, 24)
	m.handleStreamCancel()
	conv.Update(m, &m.conv, conv.AgentOutboxMsg{Event: sdkagent.InferenceUpdate{Delta: ai.Event{
		Type: ai.EventProgress, Progress: &ai.Progress{ToolName: "Write", InputBytes: 99000},
	}}})
	if m.conv.Stream.Inferring || m.conv.Stream.GeneratedBytes != 0 || len(m.conv.Tool.PendingCalls) != 0 {
		t.Fatalf("late progress revived cancelled work: %+v", m.conv.Stream)
	}
	if frame := ansi.Strip(m.View().Content); strings.Contains(frame, "Generating Write") || !strings.Contains(frame, "cancelled") {
		t.Fatalf("cancelled inference still looks active: %q", frame)
	}
}

func TestFailedInferenceCommitsAnExplicitIncompleteNotice(t *testing.T) {
	m := modelWithInferenceProgress(t, 40)
	err := errors.New("stream stalled")
	conv.Update(m, &m.conv, conv.AgentOutboxMsg{Event: sdkagent.InferenceEnd{Attempt: 1, Err: err}})
	conv.Update(m, &m.conv, conv.AgentOutboxMsg{Event: core.AgentStopped{Err: err}})
	printed := ansi.Strip(renderScrollbackLines(m.flush.visible))
	for _, want := range []string{"I will write the HTML now.", "Turn failed; output is incomplete", "stream stalled"} {
		if !strings.Contains(printed, want) {
			t.Fatalf("failed turn did not commit %q: %q", want, printed)
		}
	}
	if m.conv.Stream.Active || m.conv.Stream.Inferring || strings.Contains(printed, "Generating Write arguments") {
		t.Fatal("failed turn retained live progress or committed it into history")
	}
}
