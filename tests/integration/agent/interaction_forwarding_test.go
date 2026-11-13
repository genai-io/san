package subagent_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/interaction"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/tool"
	_ "github.com/genai-io/san/internal/tool/fs"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/tests/integration/testutil"
)

func TestWorkerForwardedApprovalControlsExecution(t *testing.T) {
	for _, background := range []bool{false, true} {
		for _, approve := range []bool{false, true} {
			t.Run(fmtCase(background, approve), func(t *testing.T) {
				cwd := t.TempDir()
				marker := filepath.Join(cwd, "approved-marker")
				seen := make(chan tool.AgentInteractionSource, 1)
				executor := newIntegrationExecutor(t, &testutil.FakeProvider{Responses: []llm.CompletionResponse{
					testutil.ToolCallResponse(tool.ToolBash, "worker-call", `{"command":"touch approved-marker","description":"test approval"}`),
					testutil.EndTurnResponse("done"),
				}}, cwd, "fake-model", nil)
				configureIntegrationApprovals(t, executor, func(ctx context.Context, source tool.AgentInteractionSource, request *perm.PermissionRequest, input map[string]any) interaction.PermissionDecision {
					if _, err := os.Stat(marker); !os.IsNotExist(err) {
						t.Error("tool ran before approval")
					}
					if source.WorkingDirectory != cwd || source.ToolCallID != "worker-call" || source.ParentToolCallID != "parent-call" || source.AgentID == "" || source.AgentName == "" {
						t.Errorf("wrong source: %+v", source)
					}
					if request.WorkingDirectory != cwd || request.PermissionMode == "" {
						t.Errorf("approval preview lost worker context: %+v", request)
					}
					if request.ID == "" || !request.OnceOnly || len(request.AllowRules) != 0 || request.BashMeta == nil {
						t.Errorf("incomplete request: %+v", request)
					}
					seen <- source
					return interaction.PermissionDecision{Approved: approve, Request: request}
				})
				req := tool.AgentExecRequest{Prompt: "run command", ParentToolCallID: "parent-call"}
				var taskID string
				if background {
					task, err := executor.RunBackground(req)
					if err != nil {
						t.Fatal(err)
					}
					taskID = task.GetID()
					if !task.WaitForCompletion(5 * time.Second) {
						t.Fatal("worker did not finish")
					}
				} else {
					ctx, cancel := context.WithTimeout(core.WithToolCallID(context.Background(), "parent-call"), 5*time.Second)
					defer cancel()
					req.ParentToolCallID = ""
					result, err := executor.Run(ctx, req)
					if err != nil {
						t.Fatal(err)
					}
					taskID = result.Identity.ID
				}
				select {
				case source := <-seen:
					if source.TaskID == "" || source.TaskID != taskID {
						t.Errorf("wrong task identity: %+v", source)
					}
				default:
					t.Fatal("approval was not forwarded")
				}
				_, err := os.Stat(marker)
				if approve && err != nil {
					t.Fatalf("approved tool did not execute: %v", err)
				}
				if !approve && !os.IsNotExist(err) {
					t.Fatal("rejected tool executed")
				}
			})
		}
	}
}

func fmtCase(background, approve bool) string {
	name := "foreground"
	if background {
		name = "background"
	}
	if approve {
		return name + "/approve"
	}
	return name + "/reject"
}
