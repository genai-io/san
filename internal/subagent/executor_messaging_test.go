package subagent

import (
	"context"
	"encoding/json"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	agentruntime "github.com/genai-io/san/internal/runtime"
	"github.com/genai-io/san/internal/runtime/mail"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	agenttools "github.com/genai-io/san/internal/tool/agent"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/internal/tool/toolresult"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestOwnedExecutorAcceptsStartupAndRunningMessages(t *testing.T) {
	ctx := context.Background()
	store, parent := continuationSessions(t, "owned-inbox-parent")
	started, release := make(chan struct{}), make(chan struct{})
	driver := aitest.New(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return aitest.Fails(ctx.Err())(ctx, req)
		}
		return aitest.Says("first answer")(ctx, req)
	}, aitest.Says("handled live instruction"))
	e := NewExecutor(continuationProvider{driver}, t.TempDir(), "aitest", nil)
	// Match TUI wiring: an injected task manager must not disable messaging.
	e.ConfigureOwner(task.Default(), Default(), nil, nil)
	bindContinuationSessions(t, e, store, parent)
	worker, err := e.RunBackground(tool.AgentExecRequest{
		Prompt: "original task", ParentToolCallID: "launch",
		OnIdentity: func(identity toolresult.AgentIdentity) {
			result := agenttools.NewSendMessageTool().Execute(task.WithSessionID(ctx, parent.ID()), map[string]any{"to": identity.ID, "message": "startup instruction"}, ".")
			if !result.Success {
				t.Errorf("startup message rejected: %s", result.Error)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = worker.Stop()
		if !worker.WaitForCompletion(5 * time.Second) {
			t.Error("worker did not stop")
		}
		task.Default().Remove(worker.GetID())
	})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	args, _ := json.Marshal(map[string]string{"to": worker.GetID(), "message": "live instruction"})
	parentDriver := aitest.New(aitest.Asks(ai.ToolCall{ID: "live-send", Name: tool.ToolSendMessage, Input: string(args)}), aitest.Says("parent finished"))
	params := agentruntime.BuildParams{Provider: continuationProvider{parentDriver}, ModelID: "aitest", CWD: t.TempDir(), SessionID: parent.ID(), Tasks: task.Default(), Recording: parent, DisabledTools: map[string]bool{tool.ToolSendMessage: false}, PermissionRules: func(string, map[string]any) agentruntime.PermDecisionResult {
		return agentruntime.PermDecisionResult{Decision: perm.Permit, By: session.ByConfig}
	}}
	if _, err := agentruntime.RunHeadless(tool.WithMessageSource(ctx, "stale-source", 999), params, core.UserMessage("send live instruction", nil), nil); err != nil {
		t.Fatal(err)
	}
	for _, message := range parentDriver.Last().Messages {
		for _, result := range message.ToolResults() {
			if result.IsError {
				t.Errorf("running message rejected: %s", result.Content.Text())
			}
		}
	}
	close(release)
	if !worker.WaitForCompletion(5 * time.Second) {
		t.Fatal("worker did not finish")
	}
	info := worker.GetStatus()
	if info.Status != task.StatusCompleted || info.Attempt != 1 {
		t.Fatalf("message restarted or failed worker: %+v", info)
	}
	snapshot := parent.Snapshot()
	source, ok := snapshot.ToolCallSource("live-send")
	if !ok {
		t.Fatal("parent tool call source missing")
	}
	child, err := store.Read(ctx, info.AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	accepted, consumed := 0, 0
	for _, event := range child.Events() {
		payload, ok := event.Payload.(*session.MessageAppendPayload)
		if !ok {
			continue
		}
		for _, message := range payload.Messages {
			if message.Role != ai.RoleUser || !strings.Contains(message.Text(), "live instruction") {
				continue
			}
			if payload.Accepted {
				accepted++
				if event.Source == nil || *event.Source != source {
					t.Errorf("wrong live input source: %+v, want %+v", event.Source, source)
				}
			} else {
				consumed++
			}
		}
	}
	if accepted != 1 || consumed != 1 {
		t.Fatalf("live input accepted=%d consumed=%d", accepted, consumed)
	}
	startup := 0
	for _, message := range driver.Last().Messages {
		if strings.Contains(message.Text(), "startup instruction") {
			startup++
		}
	}
	if startup != 1 {
		t.Fatalf("startup instruction consumed %d times", startup)
	}
}

func TestOwnedExecutorWithoutMessaging(t *testing.T) {
	store, parent := continuationSessions(t, "headless-parent")
	pool := task.NewManager()
	driver := aitest.New(aitest.Says("done"))
	e := NewExecutor(continuationProvider{driver}, t.TempDir(), "aitest", nil)
	e.ConfigureOwner(pool, Default(), nil, nil)
	e.SetMessaging(false)
	bindContinuationSessions(t, e, store, parent)
	worker, err := e.RunBackground(tool.AgentExecRequest{
		Prompt: "headless task",
		OnIdentity: func(identity toolresult.AgentIdentity) {
			if err := mail.SendErr(mail.Message{To: identity.ID}); err != mail.ErrNoRecipient {
				t.Errorf("headless launch published a route: %v", err)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Stop(); pool.Join() })
	if !worker.WaitForCompletion(5 * time.Second) {
		t.Fatal("headless worker did not finish")
	}
	if info := worker.GetStatus(); info.Status != task.StatusCompleted || info.Output != "done" {
		t.Fatalf("headless execution failed: %+v", info)
	}
	if driver.Calls() != 1 {
		t.Fatalf("headless execution called the model %d times", driver.Calls())
	}
}
