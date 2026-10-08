package app

import (
	"context"
	"testing"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/todo"
)

func TestWorkflowStopBypassesStreamingInputQueue(t *testing.T) {
	manager := task.NewManager()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wf := manager.CreateAgentTask("workflow-stop-now", "workflow", "/workflow run audit", ctx, cancel)
	m := &model{
		services:  services{Task: manager, Subagent: subagent.NewRegistry(), Tracker: todo.NewStore()},
		conv:      conv.NewModel(80),
		userInput: input.New("", 80, nil, input.SelectorDeps{}),
	}
	m.env.Width = 80
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
