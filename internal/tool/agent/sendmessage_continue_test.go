package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestSendMessageContinuationGate(t *testing.T) {
	for _, tc := range []struct {
		name           string
		status         task.TaskStatus
		parent, sender string
		allow, gate    bool
		want           string
	}{
		{"completed", task.StatusCompleted, "parent", "", true, true, ""},
		{"failed", task.StatusFailed, "parent", "", true, true, ""},
		{"denied", task.StatusCompleted, "parent", "", false, true, "denied by policy"},
		{"no gate", task.StatusCompleted, "parent", "", true, false, "parent permission gate"},
		{"peer", task.StatusCompleted, "parent", "peer", true, true, "owning main"},
		{"wrong owner", task.StatusCompleted, "other", "", true, true, "different parent"},
		{"stopped", task.StatusStopped, "parent", "", true, true, "deliberately stopped"},
		{"killed", task.StatusKilled, "parent", "", true, true, "deliberately stopped"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			worker := endedContinuationTask(t, tc.status)
			executor := &continuationExecutor{recordingExecutor: recordingExecutor{configOK: true}}
			sender := NewSendMessageTool()
			sender.SetExecutor(executor)
			checks := 0
			var opts []tool.AdaptOption
			if tc.gate {
				opts = append(opts, tool.WithPermissionCheck(perm.PermissionFunc(func(ctx context.Context, name string, input map[string]any) (bool, string) {
					checks++
					snapshot, ok := input["_continuation"].(continuationApproval)
					if name != tool.ToolAgent || !ok || snapshot.TaskID != worker.GetID() || snapshot.Attempt != 1 {
						t.Fatalf("wrong capability/snapshot: %s %+v", name, input)
					}
					creator := NewAgentTool()
					creator.SetExecutor(executor)
					preview, err := creator.PreparePermission(ctx, input, ".")
					if err != nil || preview.AgentMeta.PermissionMode != "explore" || !preview.AgentMeta.Background {
						t.Fatalf("lost original policy: %+v %v", preview, err)
					}
					return tc.allow, "denied by policy"
				})))
			}
			adapted := tool.AdaptTool(sender, sender.Schema(), func() string { return "." }, opts...)
			input, _ := json.Marshal(map[string]string{"to": worker.GetID(), "message": "Verify existing work"})
			ctx := task.WithSessionID(context.Background(), tc.parent)
			if tc.sender != "" {
				ctx = tool.WithAgentID(ctx, tc.sender)
			}
			result, err := adapted.Run(ctx, ai.ToolCall{ID: "followup", Name: tool.ToolSendMessage, Input: string(input)})
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) || executor.runReq.ResumeTaskID != "" {
					t.Fatalf("unexpected continuation: %+v %v %+v", result, err, executor.runReq)
				}
				return
			}
			req := executor.runReq
			if err != nil || checks != 1 || req.ResumeTaskID != worker.GetID() || req.ResumeAttempt != 1 || !req.Background || !strings.Contains(req.Prompt, "Verify existing work") || req.ResolvedAgentConfig != nil {
				t.Fatalf("lost continuation contract: %+v %v checks=%d", req, err, checks)
			}
		})
	}
}
