package subagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

type flowLifecycleObserver struct{ created, completed []task.TaskInfo }

func (o *flowLifecycleObserver) TaskCreated(i task.TaskInfo)   { o.created = append(o.created, i) }
func (o *flowLifecycleObserver) TaskCompleted(i task.TaskInfo) { o.completed = append(o.completed, i) }

func TestFlowManagedRunPublishesNativeLifecycleAndToolProgress(t *testing.T) {
	store, parent := continuationSessions(t, "flow-owner")
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "evidence.txt"), []byte("evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	d := aitest.New(aitest.Replies(ai.Response{Content: ai.Content{ai.ToolCallBlock(ai.ToolCall{ID: "read", Name: "Read", Input: `{"file_path":"evidence.txt"}`})}}), aitest.Says("actual answer"))
	e := NewExecutor(continuationProvider{d}, cwd, "aitest", nil)
	if err := e.SetSessions(store, parent); err != nil {
		t.Fatal(err)
	}
	observer := &flowLifecycleObserver{}
	task.SetLifecycleHandler(observer)
	defer task.SetLifecycleHandler(nil)
	var id string
	starts, ends := 0, 0
	result, err := e.Run(context.Background(), tool.AgentExecRequest{
		Prompt: "read evidence", Background: true, Flow: &tool.FlowOrigin{TaskID: "scheduler"},
		OnIdentity: func(identity toolresult.AgentIdentity) { id = identity.ID },
		OnToolStart: func(_, call string) {
			starts++
			child, ok := e.taskPool().Get(id)
			if !ok || child.GetStatus().Progress != call {
				t.Errorf("native start progress missing for %s", call)
			}
		},
		OnToolResult: func(_, call, output string, toolErr error) {
			ends++
			child, _ := e.taskPool().Get(id)
			if !strings.Contains(child.GetStatus().Progress, call+" · completed") || output == "" || toolErr != nil {
				t.Errorf("native result progress missing: %+v, %q, %v", child.GetStatus(), output, toolErr)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(observer.created) != 1 || len(observer.completed) != 1 {
		t.Fatalf("native lifecycle: %+v", observer)
	}
	for _, info := range []task.TaskInfo{observer.created[0], observer.completed[0]} {
		if info.ID != id || info.ParentTaskID != "scheduler" || info.ParentSessionID != parent.ID() {
			t.Fatalf("native ownership: %+v", info)
		}
	}
	if observer.created[0].Status != task.StatusRunning || observer.completed[0].Status != task.StatusCompleted || starts != 1 || ends != 1 {
		t.Fatalf("lifecycle/tool callbacks: %+v starts=%d ends=%d", observer, starts, ends)
	}
	child, _ := e.taskPool().Get(id)
	if child.GetOutput() != "actual answer" || result.Content != "actual answer" || result.Identity.ID != id || result.Identity.SessionID == "" {
		t.Fatalf("native result missing: %+v", result)
	}
	if _, err := e.RunBackground(tool.AgentExecRequest{ResumeTaskID: id, Prompt: "continue"}); err == nil || !strings.Contains(err.Error(), "scheduler-owned") {
		t.Fatalf("managed child allowed independent continuation: %v", err)
	}
	if child.GetStatus().Attempt != 1 || d.Calls() != 2 {
		t.Fatal("rejected continuation changed the worker or called the model")
	}
}
