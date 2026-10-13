package subagent_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/tool"
	_ "github.com/genai-io/san/internal/tool/fs"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/tests/integration/testutil"
)

type forwardingInteractions struct {
	approve func(context.Context, tool.AgentInteractionSource, *perm.PermissionRequest, map[string]any) (bool, string)
}

func (f forwardingInteractions) Approve(ctx context.Context, source tool.AgentInteractionSource, request *perm.PermissionRequest, input map[string]any) (bool, string) {
	return f.approve(ctx, source, request, input)
}
func (f forwardingInteractions) Ask(context.Context, tool.AgentInteractionSource, *tool.QuestionRequest) (*tool.QuestionResponse, error) {
	return nil, nil
}

func TestWorkerForwardedApprovalControlsExecution(t *testing.T) {
	for _, background := range []bool{false, true} {
		for _, approve := range []bool{false, true} {
			t.Run(fmtCase(background, approve), func(t *testing.T) {
				cwd := t.TempDir()
				marker := filepath.Join(cwd, "approved-marker")
				seen := make(chan tool.AgentInteractionSource, 1)
				executor := subagent.NewExecutor(&testutil.FakeProvider{Responses: []llm.CompletionResponse{
					testutil.ToolCallResponse(tool.ToolBash, "worker-call", `{"command":"touch approved-marker","description":"test approval","run_in_background":false}`),
					testutil.EndTurnResponse("done"),
				}}, cwd, "fake-model", nil)
				executor.SetInteractions(forwardingInteractions{approve: func(ctx context.Context, source tool.AgentInteractionSource, request *perm.PermissionRequest, input map[string]any) (bool, string) {
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
					return approve, "test decision"
				}})
				req := tool.AgentExecRequest{Prompt: "run command", ParentToolCallID: "parent-call"}
				if background {
					task, err := executor.RunBackground(req)
					if err != nil {
						t.Fatal(err)
					}
					if !task.WaitForCompletion(5 * time.Second) {
						t.Fatal("worker did not finish")
					}
				} else {
					ctx, cancel := context.WithTimeout(core.WithToolCallID(context.Background(), "parent-call"), 5*time.Second)
					defer cancel()
					req.ParentToolCallID = ""
					if _, err := executor.Run(ctx, req); err != nil {
						t.Fatal(err)
					}
				}
				select {
				case source := <-seen:
					if (source.TaskID != "") != background {
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
