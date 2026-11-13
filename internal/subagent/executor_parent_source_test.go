package subagent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	agentruntime "github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	agenttools "github.com/genai-io/san/internal/tool/agent"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestParentBuildSendMessageContinuesOriginalChildWithNativeSource(t *testing.T) {
	ctx := context.Background()
	store, parent := continuationSessions(t, "owner")
	d := aitest.New(aitest.Says("first answer"), aitest.Says("continued answer"))
	e := NewExecutor(continuationProvider{d}, t.TempDir(), "aitest", nil)
	if err := e.SetSessions(store, parent); err != nil {
		t.Fatal(err)
	}
	worker, err := e.RunBackground(tool.AgentExecRequest{Prompt: "original task", ParentToolCallID: "original-launch"})
	if err != nil {
		t.Fatal(err)
	}
	defer task.Default().Remove(worker.GetID())
	if !worker.WaitForCompletion(5 * time.Second) {
		t.Fatal("initial worker hung")
	}
	before := worker.GetStatus()
	if before.Error != "" {
		t.Fatal(before.Error)
	}
	originalTool, ok := tool.Get(tool.ToolSendMessage)
	if !ok {
		t.Fatal("SendMessage not registered")
	}
	sender := agenttools.NewSendMessageTool()
	sender.SetExecutor(NewExecutorAdapter(e))
	tool.Register(sender)
	t.Cleanup(func() { tool.Register(originalTool) })
	args, _ := json.Marshal(map[string]string{"to": worker.GetID(), "message": "inspect previous result"})
	parentDriver := aitest.New(aitest.Asks(ai.ToolCall{ID: "parent-followup", Name: tool.ToolSendMessage, Input: string(args)}), aitest.Says("parent finished"))
	params := agentruntime.BuildParams{Provider: continuationProvider{parentDriver}, ModelID: "aitest", CWD: t.TempDir(), SessionID: parent.ID(), Tasks: task.Default(), Recording: parent, DisabledTools: map[string]bool{tool.ToolSendMessage: false}, PermissionRules: func(string, map[string]any) agentruntime.PermDecisionResult {
		return agentruntime.PermDecisionResult{Decision: perm.Permit, By: session.ByConfig}
	}}
	// The build closure must replace inherited provenance with this actual call.
	_, err = agentruntime.RunOnce(tool.WithMessageSource(ctx, "stale-source", 999), params, core.UserMessage("send a follow-up", nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !worker.WaitForCompletion(5 * time.Second) {
		t.Fatal("continued worker hung")
	}
	after := worker.GetStatus()
	if after.Attempt != 2 || after.AgentSessionID != before.AgentSessionID || after.Error != "" {
		t.Fatalf("continuation identity/state lost: %+v", after)
	}
	parentSnapshot := parent.Snapshot()
	source, ok := parentSnapshot.ToolCallSource("parent-followup")
	if !ok || source.Session != parent.ID() {
		t.Fatal("missing actual parent call source")
	}
	child, err := store.Read(ctx, after.AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	link := child.Identity().Parent
	if link == nil || link.CallID != "original-launch" {
		t.Fatalf("continuation replaced original spawn: %+v", link)
	}
	var accepted *session.Event
	var delivered ai.Message
	for _, event := range child.Events() {
		if event.Source == nil || event.Source.Session != source.Session || event.Source.Seq != source.Seq {
			continue
		}
		payload, ok := event.Payload.(*session.MessageAppendPayload)
		if !ok || !payload.Accepted || len(payload.Messages) != 1 {
			t.Fatalf("missing exact accepted continuation: %+v", event)
		}
		copied := event
		accepted = &copied
		delivered = payload.Messages[0]
	}
	if accepted == nil || delivered.ID == "" || !strings.Contains(delivered.Text(), "inspect previous result") {
		t.Fatal("continuation lacked trusted foreign provenance")
	}
	consumed := 0
	for _, event := range child.Events() {
		if event.Source == nil || event.Source.Session != "" || event.Source.Seq != accepted.Seq {
			continue
		}
		if payload, ok := event.Payload.(*session.MessageAppendPayload); ok && !payload.Accepted && len(payload.Messages) == 1 && payload.Messages[0].ID == delivered.ID {
			consumed++
		}
	}
	if consumed != 1 {
		t.Fatalf("continuation consumed %d times", consumed)
	}
	sent := d.Last().Messages
	found := false
	for _, message := range sent {
		if message.ID == delivered.ID && message.Text() == delivered.Text() {
			found = true
		}
	}
	if !found {
		t.Fatal("native model input differed from accepted prompt")
	}
}
