package subagent

import (
	"context"
	"testing"
	"time"

	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestRuntimeIdentityContinuationUsesOriginalCallAndFreshCallback(t *testing.T) {
	store, err := session.NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := NewExecutor(continuationProvider{aitest.New(aitest.Says("first"), aitest.Says("second"))}, t.TempDir(), "aitest", nil)
	e.SetSessionStore(store, "identity-parent")
	adapter := NewExecutorAdapter(e)
	var first, second []toolresult.AgentIdentity
	info, err := adapter.RunBackground(tool.AgentExecRequest{Prompt: "work", ParentToolCallID: "launch", OnIdentity: func(i toolresult.AgentIdentity) { first = append(first, i) }})
	if err != nil {
		t.Fatal(err)
	}
	worker, _ := task.Default().Get(info.TaskID)
	defer task.Default().Remove(info.TaskID)
	agentTask := worker.(*task.AgentTask)
	if !agentTask.WaitForCompletion(5 * time.Second) {
		t.Fatal("first run hung")
	}
	if len(first) != 1 || first[0].ID != info.TaskID || first[0].CallID != "launch" || info.Identity.ID != first[0].ID || info.Identity.CallID != first[0].CallID {
		t.Fatalf("launch identity: %+v / %+v", info, first)
	}
	state := agentTask.LaunchState().(*continuationState)
	if state.req.OnIdentity != nil {
		t.Fatal("checkpoint retained parent callback")
	}
	resumed, err := adapter.RunBackground(tool.AgentExecRequest{ResumeTaskID: info.TaskID, Prompt: "continue", ParentToolCallID: "resume", OnIdentity: func(i toolresult.AgentIdentity) { second = append(second, i) }})
	if err != nil {
		t.Fatal(err)
	}
	if !agentTask.WaitForCompletion(5 * time.Second) {
		t.Fatal("continuation hung")
	}
	if len(first) != 1 || len(second) != 1 || second[0].ID != info.TaskID || second[0].CallID != "launch" || second[0].SessionID == "" || second[0].SessionID != agentTask.GetStatus().AgentSessionID || resumed.Identity != second[0] {
		t.Fatalf("continuation identity: first=%+v second=%+v result=%+v", first, second, resumed)
	}
}

func TestRuntimeIdentityForegroundPrecedesActivityAndPreservesSession(t *testing.T) {
	store, err := session.NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := NewExecutor(continuationProvider{aitest.New(aitest.Says("finding"))}, t.TempDir(), "aitest", nil)
	e.SetSessionStore(store, "foreground-parent")
	var identities []toolresult.AgentIdentity
	activityBeforeIdentity := false
	result, err := NewExecutorAdapter(e).Run(context.Background(), tool.AgentExecRequest{
		Prompt: "work", ParentToolCallID: "launch",
		OnIdentity: func(i toolresult.AgentIdentity) { identities = append(identities, i) },
		OnActivity: func(string) {
			if len(identities) == 0 {
				activityBeforeIdentity = true
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if activityBeforeIdentity || len(identities) != 1 || identities[0].ID == "" || identities[0].ID == result.AgentID || identities[0].CallID != "launch" || identities[0].SessionID != result.AgentID || result.AgentID == "" || result.Identity != identities[0] {
		t.Fatalf("identity=%+v result=%+v earlyActivity=%v", identities, result, activityBeforeIdentity)
	}
}

func TestRuntimeIdentityForegroundUsesHookAddressAndReportsOnce(t *testing.T) {
	var seen []toolresult.AgentIdentity
	run := &preparedRun{hookID: "foreground-hook", cfg: &runConfig{config: &AgentConfig{Name: "worker"}}, req: tool.AgentExecRequest{ParentToolCallID: "launch", OnIdentity: func(i toolresult.AgentIdentity) { seen = append(seen, i) }}}
	run.reportIdentity()
	run.reportIdentity()
	if len(seen) != 1 || seen[0] != (toolresult.AgentIdentity{ID: "foreground-hook", Name: "worker", CallID: "launch"}) {
		t.Fatalf("identity: %+v", seen)
	}
}
