package subagent

import (
	"context"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestSynchronousManagedChildHasNativeOwnershipAndProgress(t *testing.T) {
	for _, managed := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "managed"}[managed], func(t *testing.T) {
			store, parent := continuationSessions(t, "ownership-parent")
			e := NewExecutor(continuationProvider{aitest.New(aitest.Says("finding"))}, t.TempDir(), "aitest", nil)
			bindContinuationSessions(t, e, store, parent)
			var child *task.AgentTask
			var activity bool
			req := tool.AgentExecRequest{Prompt: "work", Background: true, OnIdentity: func(i toolresult.AgentIdentity) {
				worker, ok := task.Default().Get(i.ID)
				if !ok {
					t.Fatal("native task absent at identity")
				}
				child = worker.(*task.AgentTask)
				want := ""
				if managed {
					want = "scheduler"
				}
				if child.GetStatus().ParentTaskID != want {
					t.Fatalf("ownership: %+v", child.GetStatus())
				}
			}, OnActivity: func(msg string) {
				activity = true
				if managed && !strings.HasPrefix(msg, "Model: ") && !strings.HasPrefix(msg, "Mode: ") && !strings.HasPrefix(msg, "Usage: ") && child.GetStatus().Progress != strings.TrimSpace(msg) {
					t.Fatalf("native child not live: %+v", child.GetStatus())
				}
			}}
			if managed {
				req.ParentTaskID, req.Flow = "scheduler", &tool.FlowOrigin{TaskID: "scheduler", NodeID: "node"}
			}
			result, err := NewExecutorAdapter(e).Run(context.Background(), req)
			if err != nil || result == nil || !result.Success {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if child == nil || !activity {
				t.Fatal("missing native callbacks")
			}
			defer task.Default().Remove(child.GetID())
		})
	}
}
