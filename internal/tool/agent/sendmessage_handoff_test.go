package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/agent/mail"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestSendMessageMissingRouteAfterRetirement(t *testing.T) {
	for _, scenario := range []string{"completed", "failed", "running", "denied", "changed attempt", "replaced task", "peer"} {
		t.Run(scenario, func(t *testing.T) {
			mail.Reset()
			t.Cleanup(mail.Reset)
			worker := endedContinuationTask(t, task.StatusRunning)
			ctx := task.WithSessionID(context.Background(), "parent")
			if scenario == "peer" {
				ctx = tool.WithAgentID(ctx, "peer")
			}
			executor := &continuationExecutor{}
			sender := NewSendMessageTool()
			sender.SetExecutor(executor)
			checks := 0
			adapted := tool.AdaptTool(sender, sender.Schema(), func() string { return "." }, tool.WithPermissionCheck(perm.PermissionFunc(func(context.Context, string, map[string]any) (bool, string) {
				checks++
				return scenario != "denied", "denied by policy"
			})))
			calls := 0
			mail.RegisterAccept(worker.GetID(), func(mail.Message) error {
				calls++
				if scenario != "running" {
					if scenario == "failed" {
						worker.Complete(errors.New("provider failed"))
					} else {
						worker.Complete(nil)
					}
				}
				if scenario == "changed attempt" {
					if _, err := worker.BeginAttempt("parent", context.Background(), nil); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "replaced task" {
					task.Default().Remove(worker.GetID())
					task.Default().CreateAgentTask(worker.GetID(), "replacement", "new work", ctx, nil)
				}
				mail.Unregister(worker.GetID())
				return mail.ErrNoRecipient
			})
			input, _ := json.Marshal(map[string]string{"to": worker.GetID(), "message": "follow up"})
			_, err := adapted.Run(ctx, ai.ToolCall{ID: "retired-route", Name: tool.ToolSendMessage, Input: string(input)})
			if calls != 1 {
				t.Fatalf("retried missing route %d times", calls)
			}
			if scenario == "completed" || scenario == "failed" {
				if err != nil || checks != 1 || executor.runReq.ResumeTaskID != worker.GetID() || executor.runReq.ResumeAttempt != 1 {
					t.Fatalf("lost exact retirement handoff: err=%v checks=%d req=%+v", err, checks, executor.runReq)
				}
				return
			}
			if err == nil || executor.runReq.ResumeTaskID != "" {
				t.Fatalf("unsafe missing-route continuation: err=%v req=%+v", err, executor.runReq)
			}
			if scenario == "denied" {
				if checks != 1 || !strings.Contains(err.Error(), "denied by policy") {
					t.Fatalf("retirement bypassed permission: %v checks=%d", err, checks)
				}
			} else if checks != 0 {
				t.Fatalf("ineligible missing route reached Agent gate %d times", checks)
			}
		})
	}
}

func TestSendMessageClosingHandoff(t *testing.T) {
	for _, scenario := range []string{"wait", "canceled", "denied", "changed attempt", "peer", "stopped", "full during completion", "rejected during completion"} {
		t.Run(scenario, func(t *testing.T) {
			mail.Reset()
			t.Cleanup(mail.Reset)
			worker := endedContinuationTask(t, task.StatusRunning)
			ctx, cancel := context.WithCancel(task.WithSessionID(context.Background(), "parent"))
			defer cancel()
			if scenario == "peer" {
				ctx = tool.WithAgentID(ctx, "peer")
			}
			executor := &continuationExecutor{}
			sender := NewSendMessageTool()
			sender.SetExecutor(executor)
			checks := 0
			adapted := tool.AdaptTool(sender, sender.Schema(), func() string { return "." }, tool.WithPermissionCheck(perm.PermissionFunc(func(_ context.Context, name string, input map[string]any) (bool, string) {
				checks++
				if name != tool.ToolAgent || worker.GetStatus().Status != task.StatusCompleted {
					t.Errorf("permission checked before exact execution retired: %s %+v", name, worker.GetStatus())
				}
				return scenario != "denied", "denied by policy"
			})))
			entered := make(chan struct{})
			calls := 0
			mail.RegisterAccept(worker.GetID(), func(mail.Message) error {
				calls++
				switch scenario {
				case "canceled":
					cancel()
				case "denied", "full during completion", "rejected during completion":
					worker.Complete(nil)
				case "changed attempt":
					worker.Complete(nil)
					if _, err := worker.BeginAttempt("parent", context.Background(), nil); err != nil {
						t.Errorf("start competing execution: %v", err)
					}
				case "stopped":
					worker.Complete(context.Canceled)
				}
				close(entered)
				if scenario == "full during completion" {
					return mail.ErrInboxFull
				}
				if scenario == "rejected during completion" {
					return mail.ErrRejected
				}
				return mail.ErrInboxClosing
			})
			input, _ := json.Marshal(map[string]string{"to": worker.GetID(), "message": "follow up"})
			done := make(chan error, 1)
			go func() {
				_, err := adapted.Run(ctx, ai.ToolCall{ID: "handoff", Name: tool.ToolSendMessage, Input: string(input)})
				done <- err
			}()
			<-entered
			if scenario == "wait" {
				worker.Complete(nil)
			}
			err := <-done
			if calls != 1 {
				t.Fatalf("delivery attempted %d times", calls)
			}
			if scenario == "wait" {
				if err != nil || checks != 1 || executor.runReq.ResumeTaskID != worker.GetID() || executor.runReq.ResumeAttempt != 1 {
					t.Fatalf("handoff lost exact continuation: err=%v checks=%d req=%+v", err, checks, executor.runReq)
				}
				return
			}
			want := map[string]string{"canceled": "context canceled", "denied": "denied by policy", "changed attempt": "execution changed", "peer": "owning main", "stopped": "deliberately stopped", "full during completion": "recipient inbox is full", "rejected during completion": "recipient rejected the message"}[scenario]
			if err == nil || !strings.Contains(err.Error(), want) || executor.runReq.ResumeTaskID != "" {
				t.Fatalf("unsafe handoff: err=%v req=%+v, want %q", err, executor.runReq, want)
			}
			if scenario != "denied" && checks != 0 {
				t.Fatalf("ineligible handoff reached Agent gate %d times", checks)
			}
		})
	}
}
