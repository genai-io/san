package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/tui/chat"
	"github.com/genai-io/san/internal/app/tui/input"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/flow"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/todo"
)

func flowInputModel(manager *task.Manager) *model {
	m := &model{
		services:  services{Task: manager, Flow: flow.NewService(manager), Subagent: subagent.NewRegistry(), Tracker: todo.NewStore()},
		conv:      chat.NewModel(80),
		userInput: input.New("", 80, nil, input.SelectorDeps{}),
	}
	m.env.Width = 80
	return m
}

func TestReleasedPromptRecall(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m, _ := compactQueueModel(t)
	const prompt = "first line\nsecond line"
	const draft = "unfinished reply"
	m.conv.Stream.Active = true
	m.userInput.Textarea.SetValue(prompt)
	m.handleSubmit()
	if len(m.userInput.History.Items) != 0 || m.userInput.Queue.Len() != 1 {
		t.Fatal("editable prompt should still be queued, not in recall history")
	}
	m.userInput.Textarea.SetValue(draft)
	if _, ok := m.releaseQueuedMessage(); !ok {
		t.Fatal("queued prompt was not released")
	}
	m.handleTextareaShortcut(tea.KeyPressMsg{Code: tea.KeyUp})
	if got := m.userInput.Textarea.Value(); got != prompt || len(m.userInput.History.Items) != 1 {
		t.Fatalf("Up recalled %q; history = %q", got, m.userInput.History.Items)
	}
	m.handleTextareaShortcut(tea.KeyPressMsg{Code: tea.KeyDown})
	if got := m.userInput.Textarea.Value(); got != draft {
		t.Fatalf("Down restored %q, want %q", got, draft)
	}
}

func TestFlowStopBypassesStreamingInputQueue(t *testing.T) {
	manager := task.NewManager()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wf := manager.CreateSchedulerTask("flow-stop-now", "flow", "/flow run audit", ctx, cancel)
	m := flowInputModel(manager)
	m.conv.Stream.Active = true
	m.userInput.Textarea.SetValue("/flow stop flow-stop-now")
	m.handleSubmit()

	if ctx.Err() != context.Canceled {
		t.Fatalf("flow context = %v; stop was queued behind the foreground turn", ctx.Err())
	}
	if m.userInput.Queue.Len() != 0 {
		t.Fatal("stop command entered the input queue")
	}
	if m.userInput.Textarea.Value() != "" {
		t.Fatal("stop command remained in the textarea")
	}
	wf.Complete(ctx.Err())
}

func TestFlowStopPreservesStreamingAssistant(t *testing.T) {
	for _, tc := range []struct {
		name, command, notice string
		canceled              bool
	}{
		{"stop", "/flow stop flow-stream", "Stopping flow flow-stream", true},
		{"error", "/flow stop missing", "Error: running flow missing not found", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := task.NewManager()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			wf := manager.CreateSchedulerTask("flow-stream", "flow", "audit", ctx, cancel)
			defer func() { wf.Stop(); wf.Complete(ctx.Err()) }()
			m := flowInputModel(manager)
			m.conv.Stream.Active = true
			assistant := m.conv.Append(core.ChatMessage{Role: core.ChatAssistant, Content: "Before "})
			m.conv.Stream.MessageID = assistant.ID
			m.userInput.Textarea.SetValue(tc.command)

			m.handleSubmit()
			if got := ctx.Err() == context.Canceled; got != tc.canceled {
				t.Fatalf("flow canceled = %v, want %v", got, tc.canceled)
			}
			if len(m.conv.Messages) != 2 || m.conv.CommittedCount != 0 || !strings.Contains(m.conv.Messages[1].Content, tc.notice) {
				t.Fatalf("stop notice missing or committed the stream: messages=%+v committed=%d", m.conv.Messages, m.conv.CommittedCount)
			}
			if m.userInput.Queue.Len() != 0 || m.userInput.Textarea.Value() != "" {
				t.Fatal("stop was queued or left in the input")
			}
			m.conv.AppendStream("after", "")
			m.conv.SetStreamToolCalls([]core.ToolCall{{ID: "read-1", Name: "Read"}})
			m.conv.SetStreamThinkingSignature("signature")
			got := m.conv.Messages[0]
			if got.ID != assistant.ID || got.Content != "Before after" || len(got.ToolCalls) != 1 || got.ThinkingSignature != "signature" {
				t.Fatalf("stream deltas or metadata were lost: %+v", got)
			}

			m.conv.Stream.Stop()
			m.CommitMessages()
			if m.conv.CommittedCount != 2 {
				t.Fatalf("stop notice was not committed: %+v", m.conv.Messages)
			}
		})
	}
}

func TestCtrlCStopsSoleIdleFlow(t *testing.T) {
	manager := task.NewManager()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wf := manager.CreateSchedulerTask("flow-ctrl-c", "flow", "/flow run audit", ctx, cancel)
	m := flowInputModel(manager)
	m.userInput.LastCtrlC = time.Now() // A stop must disarm even a pending exit shortcut.

	_, handled := m.handleTextareaShortcut(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !handled || ctx.Err() != context.Canceled {
		t.Fatalf("Ctrl+C handled=%v, flow context=%v; want immediate stop", handled, ctx.Err())
	}
	if !m.userInput.LastCtrlC.IsZero() {
		t.Fatal("flow stop left the exit shortcut armed")
	}
	if len(m.conv.Messages) == 0 || !strings.Contains(m.conv.Messages[len(m.conv.Messages)-1].Content, "Stopping flow flow-ctrl-c") {
		t.Fatalf("stop notice missing: %+v", m.conv.Messages)
	}
	wf.Complete(ctx.Err())
	_, handled = m.handleTextareaShortcut(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !handled || m.userInput.LastCtrlC.IsZero() {
		t.Fatal("Ctrl+C should arm exit after the flow has stopped")
	}
}

func TestCtrlCDoesNotGuessBetweenMultipleFlows(t *testing.T) {
	manager := task.NewManager()
	ctxA, cancelA := context.WithCancel(context.Background())
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelA()
	defer cancelB()
	a := manager.CreateSchedulerTask("flow-a", "flow", "audit a", ctxA, cancelA)
	b := manager.CreateSchedulerTask("flow-b", "flow", "audit b", ctxB, cancelB)
	m := flowInputModel(manager)

	m.handleTextareaShortcut(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if ctxA.Err() != nil || ctxB.Err() != nil {
		t.Fatal("Ctrl+C chose a flow when several were running")
	}
	if !m.userInput.LastCtrlC.IsZero() {
		t.Fatal("multiple flow hint armed exit")
	}
	if len(m.conv.Messages) == 0 || !strings.Contains(m.conv.Messages[len(m.conv.Messages)-1].Content, "Several flows are running") {
		t.Fatalf("task ID hint missing: %+v", m.conv.Messages)
	}
	a.Stop()
	b.Stop()
	a.Complete(ctxA.Err())
	b.Complete(ctxB.Err())
}

func TestCtrlCStopsForegroundBeforeBackgroundFlow(t *testing.T) {
	manager := task.NewManager()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wf := manager.CreateSchedulerTask("flow-in-background", "flow", "audit", ctx, cancel)
	m := flowInputModel(manager)
	m.services.Agent = &agent.Runtime{}
	m.conv.Stream.Active = true

	m.handleTextareaShortcut(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if m.conv.Stream.Active {
		t.Fatal("foreground stream stayed active")
	}
	if ctx.Err() != nil {
		t.Fatal("first Ctrl+C stopped the background flow instead of the foreground turn")
	}
	wf.Stop()
	wf.Complete(ctx.Err())
}
