package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/reminder"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/tool"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestRecallNoticePinsToReceivingResultWhenWriterRunsAhead(t *testing.T) {
	mem := t.TempDir()
	prev := conv.MemoryStores
	conv.MemoryStores = system.MemoryStores{Project: mem}
	t.Cleanup(func() { conv.MemoryStores = prev })
	path := filepath.Join(mem, "release.md")
	if err := os.WriteFile(path, []byte("---\ndescription: Release policy\n---\nPrivate release body"), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := session.NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rec := mustCreateSession(t, store, session.CreateOptions{ID: "memory-anchor", AgentID: "main"})
	t.Cleanup(func() { _ = rec.Close() })
	attached := "source body\n\n" + reminder.WrapWithSource("Memories relevant to this message:\n\nMemory `"+path+"` (project store):\nPrivate release body", reminder.ProviderMemoryRecall)
	call := ai.ToolCall{ID: "call", Name: "Read", Input: `{"file_path":"main.go"}`}
	result := core.ToolResultMessage(core.ToolResult{ToolCallID: call.ID, ToolName: "Read", Content: ai.TextContent(attached)})
	result.ID = "result"
	messages := []core.Message{
		{ID: "user", Role: ai.RoleUser, Content: ai.TextContent("submit the PR")},
		{ID: "assistant", Role: ai.RoleAssistant, Content: ai.Content{ai.ToolCallBlock(call)}},
		result,
		{ID: "answer", Role: ai.RoleAssistant, Content: ai.TextContent("Final answer")},
	}
	// The writer can already have the answer before the UI consumes the
	// earlier result's event. The recall must not anchor to that latest row.
	for _, message := range messages {
		rec.RecordEvent(sdkagent.MessageAdded{Message: message})
	}
	m := fixedComposerModel(80, 40)
	m.services.Agent, m.services.Session = &agent.Runtime{}, rec
	m.conv.OnNotice = m.recordNotice
	m.conv.Messages = session.ChatRows(messages[:3])
	conv.Update(m, &m.conv, conv.AgentOutboxMsg{Event: sdkagent.MessageAdded{Message: result}})
	history, err := store.Read("memory-anchor")
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Notices) != 1 || history.Notices[0].AfterMessageID != result.ID {
		t.Fatalf("recall attached to the writer's latest row instead of its result: %+v", history.Notices)
	}
	rows := history.ChatRows()
	p := conv.RenderContext{Width: 80, Messages: rows, InlinedResults: conv.PrecomputeInlinedResults(rows, 0)}
	out := ansi.Strip(conv.RenderMessageRange(p, 0, len(rows), false))
	at, answer := strings.Index(out, "⟡ Memory · recalled: Release policy"), strings.Index(out, "Final answer")
	if at < 0 || answer <= at || strings.Count(out, "Release policy") != 1 || strings.Contains(out, "Private release body") {
		t.Fatalf("resumed recall moved to the footer or leaked its reminder: %q", out)
	}
}

func TestMemoryReadsCommitBeforeAnswerWithoutTurnEndFooter(t *testing.T) {
	for _, withRecall := range []bool{false, true} {
		name := "explicit read"
		if withRecall {
			name = "read and automatic recall"
		}
		t.Run(name, func(t *testing.T) {
			mem := t.TempDir()
			prev := conv.MemoryStores
			conv.MemoryStores = system.MemoryStores{Project: mem}
			t.Cleanup(func() { conv.MemoryStores = prev })
			path := filepath.Join(mem, "sign-off.md")
			if err := os.WriteFile(path, []byte("---\ndescription: Sign off PR commits\n---\nPrivate convention body"), 0600); err != nil {
				t.Fatal(err)
			}
			recallPath := filepath.Join(mem, "release.md")
			if err := os.WriteFile(recallPath, []byte("---\ndescription: Release policy\n---\nPrivate release body"), 0600); err != nil {
				t.Fatal(err)
			}
			m := fixedComposerModel(80, 40)
			m.services.Agent = &agent.Runtime{}
			m.services.Tool = tool.NewRegistry()
			m.pendingDecisions = &sync.Map{}
			var notices []core.ChatMessage
			m.conv.OnNotice = func(msg core.ChatMessage) { notices = append(notices, msg) }
			opened := m.conv.Append(core.ChatMessage{Role: core.ChatUser, Content: "submit this PR"})
			m.turnOpenedBy = opened.ID
			deliver := func(event core.Event) {
				t.Helper()
				if _, ok := conv.Update(m, &m.conv, conv.AgentOutboxMsg{Event: event}); !ok {
					t.Fatalf("event %T was not handled", event)
				}
			}
			input, _ := json.Marshal(map[string]string{"file_path": path})
			call := ai.ToolCall{ID: "read", Name: "Read", Input: string(input)}
			deliver(sdkagent.InferenceStart{})
			deliver(sdkagent.InferenceEnd{Response: &ai.Response{Content: ai.Content{ai.ToolCallBlock(call)}}})
			deliver(sdkagent.ToolEnd{ID: call.ID, Name: call.Name, Result: sdkagent.TextResult("Private convention body")})
			modelOutput := "Private convention body"
			if withRecall {
				modelOutput += "\n\n" + reminder.WrapWithSource("Memories relevant to this message:\n\nMemory `"+recallPath+"` (project store, saved 2026-10-24):\nPrivate release body", reminder.ProviderMemoryRecall)
			}
			deliver(sdkagent.MessageAdded{Message: core.ToolResultMessage(core.ToolResult{ToolCallID: call.ID, ToolName: "Read", Content: ai.TextContent(modelOutput)})})
			deliver(sdkagent.InferenceStart{})
			deliver(sdkagent.InferenceUpdate{Delta: ai.Event{Type: ai.EventBlockDelta, Block: ai.Block{Type: ai.BlockText, Text: "PR submitted"}}})
			deliver(sdkagent.InferenceEnd{Response: &ai.Response{Content: ai.TextContent("PR submitted")}})
			beforeEnd := len(m.conv.Messages)
			deliver(core.TurnEnded{Result: core.Result{StopReason: core.StopEndTurn}})
			if len(m.conv.Messages) != beforeEnd {
				t.Fatal("a read-only turn added a footer or empty notice at turn end")
			}
			printed := ansi.Strip(renderScrollbackLines(m.flush.visible))
			readAt, answerAt := strings.Index(printed, "⟡ Memory · read: Sign off PR commits"), strings.Index(printed, "PR submitted")
			if readAt < 0 || answerAt <= readAt || strings.Count(printed, "Sign off PR commits") != 1 {
				t.Fatalf("memory read moved behind the answer or was duplicated: %q", printed)
			}
			if withRecall {
				recalledAt := strings.Index(printed, "⟡ Memory · recalled: Release policy")
				if recalledAt <= readAt || recalledAt >= answerAt || len(notices) != 1 {
					t.Fatalf("automatic recall lost its contextual, persisted notice: %q / %+v", printed, notices)
				}
			} else if len(notices) != 0 {
				t.Fatalf("an explicit read produced an extra persisted footer: %+v", notices)
			}
			if strings.Contains(printed, "Private convention body") || strings.Contains(printed, "Private release body") || strings.Contains(printed, "◇ used") {
				t.Fatalf("compact memory display leaked its body or old footer: %q", printed)
			}
		})
	}
}
