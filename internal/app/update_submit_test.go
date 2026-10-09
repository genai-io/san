package app

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/todo"
)

func workflowInputModel(manager *task.Manager) *model {
	m := &model{
		services:  services{Task: manager, Subagent: subagent.NewRegistry(), Tracker: todo.NewStore()},
		conv:      conv.NewModel(80),
		userInput: input.New("", 80, nil, input.SelectorDeps{}),
	}
	m.env.Width = 80
	return m
}

func TestWorkflowStopBypassesStreamingInputQueue(t *testing.T) {
	manager := task.NewManager()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wf := manager.CreateAgentTask("workflow-stop-now", "workflow", "/workflow run audit", ctx, cancel)
	m := workflowInputModel(manager)
	m.conv.Stream.Active = true
	m.userInput.Textarea.SetValue("/workflow stop workflow-stop-now")
	m.handleSubmit()

	if ctx.Err() != context.Canceled {
		t.Fatalf("workflow context = %v; stop was queued behind the foreground turn", ctx.Err())
	}
	if m.userInput.Queue.Len() != 0 {
		t.Fatal("stop command entered the input queue")
	}
	if m.userInput.Textarea.Value() != "" {
		t.Fatal("stop command remained in the textarea")
	}
	wf.Complete(ctx.Err())
}

func TestWorkflowStopPreservesStreamingAssistant(t *testing.T) {
	for _, tc := range []struct {
		name, command, notice string
		canceled              bool
	}{
		{"stop", "/workflow stop workflow-stream", "Stopping workflow workflow-stream", true},
		{"error", "/workflow stop missing", "Error: workflow task missing not found", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := task.NewManager()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			wf := manager.CreateAgentTask("workflow-stream", "workflow", "audit", ctx, cancel)
			defer func() { wf.Stop(); wf.Complete(ctx.Err()) }()
			m := workflowInputModel(manager)
			m.conv.Stream.Active = true
			assistant := m.conv.Append(core.ChatMessage{Role: core.ChatAssistant, Content: "Before "})
			m.userInput.Textarea.SetValue(tc.command)

			m.handleSubmit()
			if got := ctx.Err() == context.Canceled; got != tc.canceled {
				t.Fatalf("workflow canceled = %v, want %v", got, tc.canceled)
			}
			if len(m.conv.Messages) != 1 || m.conv.CommittedCount != 0 || len(m.heldNotices) != 1 {
				t.Fatalf("stop displaced or committed the stream: messages=%+v committed=%d held=%+v", m.conv.Messages, m.conv.CommittedCount, m.heldNotices)
			}
			if m.userInput.Queue.Len() != 0 || m.userInput.Textarea.Value() != "" {
				t.Fatal("stop was queued or left in the input")
			}
			m.conv.AppendToLast("after", "")
			m.conv.SetLastToolCalls([]core.ToolCall{{ID: "read-1", Name: "Read"}})
			m.conv.SetLastThinkingSignature("signature")
			got := m.conv.Messages[0]
			if got.ID != assistant.ID || got.Content != "Before after" || len(got.ToolCalls) != 1 || got.ThinkingSignature != "signature" {
				t.Fatalf("stream deltas or metadata were lost: %+v", got)
			}

			m.conv.Stream.Stop()
			m.showHeldNotices()
			if len(m.heldNotices) != 0 || len(m.conv.Messages) != 2 || !strings.Contains(m.conv.Messages[1].Content, tc.notice) {
				t.Fatalf("stop notice was not released: %+v", m.conv.Messages)
			}
		})
	}
}

func TestCtrlCStopsSoleIdleWorkflow(t *testing.T) {
	manager := task.NewManager()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wf := manager.CreateAgentTask("workflow-ctrl-c", "workflow", "/workflow run audit", ctx, cancel)
	m := workflowInputModel(manager)
	m.userInput.LastCtrlC = time.Now() // A stop must disarm even a pending exit shortcut.

	_, handled := m.handleTextareaShortcut(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !handled || ctx.Err() != context.Canceled {
		t.Fatalf("Ctrl+C handled=%v, workflow context=%v; want immediate stop", handled, ctx.Err())
	}
	if !m.userInput.LastCtrlC.IsZero() {
		t.Fatal("workflow stop left the exit shortcut armed")
	}
	if len(m.conv.Messages) == 0 || !strings.Contains(m.conv.Messages[len(m.conv.Messages)-1].Content, "Stopping workflow workflow-ctrl-c") {
		t.Fatalf("stop notice missing: %+v", m.conv.Messages)
	}
	wf.Complete(ctx.Err())
	_, handled = m.handleTextareaShortcut(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !handled || m.userInput.LastCtrlC.IsZero() {
		t.Fatal("Ctrl+C should arm exit after the workflow has stopped")
	}
}

func TestCtrlCDoesNotGuessBetweenMultipleWorkflows(t *testing.T) {
	manager := task.NewManager()
	ctxA, cancelA := context.WithCancel(context.Background())
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelA()
	defer cancelB()
	a := manager.CreateAgentTask("workflow-a", "workflow", "audit a", ctxA, cancelA)
	b := manager.CreateAgentTask("workflow-b", "workflow", "audit b", ctxB, cancelB)
	m := workflowInputModel(manager)

	m.handleTextareaShortcut(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if ctxA.Err() != nil || ctxB.Err() != nil {
		t.Fatal("Ctrl+C chose a workflow when several were running")
	}
	if !m.userInput.LastCtrlC.IsZero() {
		t.Fatal("multiple workflow hint armed exit")
	}
	if len(m.conv.Messages) == 0 || !strings.Contains(m.conv.Messages[len(m.conv.Messages)-1].Content, "Several workflows are running") {
		t.Fatalf("task ID hint missing: %+v", m.conv.Messages)
	}
	a.Stop()
	b.Stop()
	a.Complete(ctxA.Err())
	b.Complete(ctxB.Err())
}

func TestCtrlCStopsForegroundBeforeBackgroundWorkflow(t *testing.T) {
	manager := task.NewManager()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wf := manager.CreateAgentTask("workflow-in-background", "workflow", "audit", ctx, cancel)
	m := workflowInputModel(manager)
	m.services.Agent = &agent.Session{}
	m.conv.Stream.Active = true

	m.handleTextareaShortcut(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if m.conv.Stream.Active {
		t.Fatal("foreground stream stayed active")
	}
	if ctx.Err() != nil {
		t.Fatal("first Ctrl+C stopped the background workflow instead of the foreground turn")
	}
	wf.Stop()
	wf.Complete(ctx.Err())
}
