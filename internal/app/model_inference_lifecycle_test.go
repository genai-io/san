package app

import (
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestSDKInferenceLifecycleCommitsAssistantAndNotices(t *testing.T) {
	for _, withTools := range []bool{false, true} {
		name := "text"
		if withTools {
			name = "tools"
		}
		t.Run(name, func(t *testing.T) {
			m := fixedComposerModel(80, 40)
			m.services.Agent = &agent.Runtime{}
			m.services.Tool = tool.NewRegistry()
			m.pendingDecisions = &sync.Map{}
			deliver := func(event core.Event) {
				t.Helper()
				if _, handled := conv.Update(m, &m.conv, conv.AgentOutboxMsg{Event: event}); !handled {
					t.Fatalf("event %T was not handled", event)
				}
			}
			deliver(sdkagent.InferenceStart{})
			owner := m.conv.Stream.MessageID
			m.showNoticeLine(mainNotice{FromAgent: true, Display: "background completed"})
			for _, event := range []ai.Event{
				{Type: ai.EventBlockStart, Block: ai.Block{Type: ai.BlockText}},
				{Type: ai.EventBlockDelta, Block: ai.Block{Type: ai.BlockText, Text: "Original assistant reply"}},
				{Type: ai.EventBlockEnd, Block: ai.Block{Type: ai.BlockText}},
			} {
				deliver(sdkagent.InferenceUpdate{Delta: event})
			}
			m.showNoticeLine(mainNotice{Display: "background failure"})
			if len(m.CommitMessages()) != 0 || m.conv.CommittedCount != 0 {
				t.Fatal("notices committed the assistant before inference finished")
			}
			response := &ai.Response{Content: ai.Content{{Type: ai.BlockThinking, Signature: "final-signature"}}}
			if withTools {
				response.Content = append(response.Content, ai.Block{Type: ai.BlockToolCall, ToolCall: &ai.ToolCall{
					ID: "read-1", Name: tool.ToolRead, Input: `{"file_path":"example.go"}`,
				}})
			}
			// sdk-go forwards InferenceEnd, not the EventDone it consumes internally.
			deliver(sdkagent.InferenceEnd{Response: response})
			assistant := m.conv.Messages[0]
			if assistant.ID != owner || assistant.Content != "Original assistant reply" || assistant.ThinkingSignature != "final-signature" {
				t.Fatalf("inference metadata attached to the wrong row: %+v", assistant)
			}
			if !m.conv.Stream.Active || m.conv.Stream.MessageID != "" {
				t.Fatalf("InferenceEnd should release identity, not end the turn: %+v", m.conv.Stream)
			}
			if withTools {
				if len(assistant.ToolCalls) != 1 || assistant.ToolCalls[0].ID != "read-1" || len(m.conv.Tool.PendingCalls) != 1 {
					t.Fatalf("successful call not installed and tracked: %+v / %+v", assistant, m.conv.Tool)
				}
				if len(m.CommitMessages()) != 0 || m.conv.CommittedCount != 0 {
					t.Fatal("pending tools allowed premature commit")
				}
				deliver(sdkagent.ToolStart{ID: "read-1", Name: tool.ToolRead, Args: `{"file_path":"example.go"}`})
				deliver(sdkagent.ToolEnd{ID: "read-1", Name: tool.ToolRead, Result: sdkagent.TextResult("source contents")})
				if len(m.conv.Tool.PendingCalls) != 0 || m.conv.Messages[3].ToolResult == nil || m.conv.Messages[3].ToolResult.IsError {
					t.Fatal("successful tool result did not complete its batch")
				}
				// The SDK continues with another inference before a successful turn ends.
				deliver(sdkagent.InferenceStart{})
				deliver(sdkagent.InferenceUpdate{Delta: ai.Event{Type: ai.EventBlockDelta, Block: ai.Block{Type: ai.BlockText, Text: "Review finished"}}})
				deliver(sdkagent.InferenceEnd{Response: &ai.Response{Content: ai.TextContent("Review finished")}})
			} else if len(assistant.ToolCalls) != 0 || len(m.conv.Tool.PendingCalls) != 0 {
				t.Fatal("text-only inference installed tools")
			}
			deliver(core.TurnEnded{Result: core.Result{StopReason: core.StopEndTurn}})
			if m.conv.Stream.Active || m.conv.Stream.MessageID != "" || len(m.conv.Tool.PendingCalls) != 0 || m.conv.CommittedCount != len(m.conv.Messages) {
				t.Fatalf("real OnTurnEnd did not commit all rows: stream=%+v, committed=%d/%d", m.conv.Stream, m.conv.CommittedCount, len(m.conv.Messages))
			}
			for _, notice := range m.conv.Messages[1:3] {
				if notice.ThinkingSignature != "" || len(notice.ToolCalls) != 0 {
					t.Fatalf("assistant metadata leaked to a notice: %+v", notice)
				}
			}
			printed := ansi.Strip(renderScrollbackLines(m.flush.visible))
			last := -1
			for _, text := range []string{"Original assistant reply", "background completed", "background failure"} {
				index := strings.Index(printed, text)
				if index <= last || strings.Count(printed, text) != 1 {
					t.Fatalf("committed rows missing, duplicated or out of order (%q): %q", text, printed)
				}
				last = index
			}
			before := printed
			m.CommitMessages()
			if got := ansi.Strip(renderScrollbackLines(m.flush.visible)); got != before {
				t.Fatal("committing again duplicated settled output")
			}
		})
	}
}
