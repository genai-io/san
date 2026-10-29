package subagent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/broker"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	agenttools "github.com/genai-io/san/internal/tool/agent"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/internal/tool/toolresult"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestSendMessageContinuesForegroundSession(t *testing.T) {
	store, err := session.NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	driver := aitest.New(aitest.Says("original findings"), aitest.Says("follow-up verified"))
	executor := NewExecutor(continuationProvider{driver}, t.TempDir(), "aitest", nil)
	executor.SetSessionStore(store, "message-parent")
	first, err := executor.Run(context.Background(), tool.AgentExecRequest{Prompt: "Inspect original task", MaxSteps: 3})
	if err != nil || first == nil || !first.Success {
		t.Fatalf("first execution: %+v %v", first, err)
	}
	worker, ok := task.Default().Get(first.Identity.ID)
	if !ok {
		t.Fatal("foreground has no retained identity")
	}
	t.Cleanup(func() { task.Default().Remove(first.Identity.ID) })
	before := worker.GetStatus()
	sender := agenttools.NewSendMessageTool()
	sender.SetExecutor(NewExecutorAdapter(executor))
	allowed := false
	adapted := tool.AdaptTool(sender, sender.Schema(), func() string { return "." }, tool.WithPermissionCheck(perm.PermissionFunc(func(context.Context, string, map[string]any) (bool, string) { return allowed, "denied by Agent policy" })))
	input, _ := json.Marshal(map[string]string{"to": first.Identity.ID, "message": "Verify original findings"})
	call := ai.ToolCall{ID: "follow-up", Name: tool.ToolSendMessage, Input: string(input)}
	ctx := task.WithSessionID(context.Background(), "message-parent")
	if _, err := adapted.Run(ctx, call); err == nil || worker.GetStatus().Attempt != 1 {
		t.Fatal("message bypassed Agent permission")
	}
	allowed = true
	if _, err := adapted.Run(ctx, call); err != nil {
		t.Fatal(err)
	}
	if !worker.(*task.AgentTask).WaitForCompletion(5 * time.Second) {
		t.Fatal("follow-up hung")
	}
	after := worker.GetStatus()
	if after.Status != task.StatusCompleted || after.Attempt != 2 || after.AgentSessionID != before.AgentSessionID || after.ID != before.ID {
		t.Fatalf("session replaced: before=%+v after=%+v", before, after)
	}
	var history strings.Builder
	for _, message := range driver.Last().Messages {
		history.WriteString(message.Text())
	}
	for _, want := range []string{"Inspect original task", "original findings", "Verify original findings"} {
		if !strings.Contains(history.String(), want) {
			t.Fatalf("lost history %q: %s", want, history.String())
		}
	}
	if err := task.Default().KillAttempt(after.ID, after.Attempt); err != nil {
		t.Fatal(err)
	}
	if _, err := adapted.Run(ctx, call); err == nil || worker.GetStatus().Attempt != 2 {
		t.Fatal("message revived stopped agent")
	}
}

func TestSendMessageApprovalCannotResumeLaterAttempt(t *testing.T) {
	store, err := session.NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	executor := NewExecutor(continuationProvider{aitest.New(aitest.Says("done"))}, t.TempDir(), "aitest", nil)
	executor.SetSessionStore(store, "owner")
	first, err := executor.Run(context.Background(), tool.AgentExecRequest{Prompt: "work"})
	if err != nil {
		t.Fatal(err)
	}
	worker, _ := task.Default().Get(first.Identity.ID)
	t.Cleanup(func() { task.Default().Remove(first.Identity.ID) })
	sender := agenttools.NewSendMessageTool()
	sender.SetExecutor(NewExecutorAdapter(executor))
	adapted := tool.AdaptTool(sender, sender.Schema(), func() string { return "." }, tool.WithPermissionCheck(perm.PermissionFunc(func(context.Context, string, map[string]any) (bool, string) {
		a := worker.(*task.AgentTask)
		n, err := a.BeginAttempt("owner", context.Background(), func() {})
		if err != nil {
			t.Fatal(err)
		}
		a.CompleteAttempt(n, nil)
		return true, ""
	})))
	input, _ := json.Marshal(map[string]string{"to": first.Identity.ID, "message": "verify"})
	_, err = adapted.Run(task.WithSessionID(context.Background(), "owner"), ai.ToolCall{Name: tool.ToolSendMessage, Input: string(input)})
	if err == nil || !strings.Contains(err.Error(), "changed before continuation") || worker.GetStatus().Attempt != 2 {
		t.Fatalf("stale approval executed: %v %+v", err, worker.GetStatus())
	}
}

func TestRunningAgentProcessesAcceptedMessagesWithinBudget(t *testing.T) {
	for _, budget := range []int{1, 2} {
		t.Run(string(rune('0'+budget)), func(t *testing.T) {
			driver := aitest.New(aitest.Says("first response"), aitest.Says("follow-up response"))
			executor := NewExecutor(continuationProvider{driver}, t.TempDir(), "aitest", nil)
			var route string
			sent := false
			result, err := executor.Run(context.Background(), tool.AgentExecRequest{
				Prompt: "Initial work", MaxSteps: budget,
				OnIdentity: func(identity toolresult.AgentIdentity) { route = identity.ID },
				OnTextEnd: func(error) {
					if !sent {
						sent = true
						if !broker.Send(broker.Message{To: route, From: broker.Main, Content: "new input"}) {
							t.Error("running inbox rejected input")
						}
					}
				},
			})
			t.Cleanup(func() { task.Default().Remove(route) })
			if err != nil || result.StepCount != budget || result.Success != (budget == 2) {
				t.Fatalf("budget or input lost: %+v %v", result, err)
			}
			if broker.Send(broker.Message{To: route, From: broker.Main, Content: "late input"}) {
				t.Fatal("closed execution accepted mail")
			}
		})
	}
}
