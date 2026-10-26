package subagent_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
	workflowtool "github.com/genai-io/san/internal/tool/workflow"
	"github.com/genai-io/san/tests/integration/testutil"
)

// Each worker reaches the real executor's approval bridge before any reply is
// released. Cancelling one run must leave the other run's replies independent.
func TestWorkflowInteractionOriginAndRoutingIsolation(t *testing.T) {
	if err := task.Initialize(task.Options{OutputDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, bg := range task.Default().ListRunning() {
			_ = bg.Stop()
			if !bg.WaitForCompletion(5 * time.Second) {
				t.Errorf("workflow %s did not stop during cleanup", bg.GetID())
			}
		}
		task.ResetDefaultTracker()
	})
	responses := make([]llm.CompletionResponse, 0, 8)
	for range 4 {
		responses = append(responses, testutil.ToolCallResponse(tool.ToolBash, "worker-call", `{"command":"touch approved-marker","description":"test workflow approval","execution_mode":"foreground"}`))
	}
	for range 4 {
		responses = append(responses, testutil.EndTurnResponse("done"))
	}
	provider := &testutil.FakeProvider{Responses: responses}
	executor := subagent.NewExecutor(provider, t.TempDir(), "fake-model", nil)
	type pendingApproval struct {
		source    tool.AgentInteractionSource
		reply     chan bool
		cancelled chan struct{}
	}
	pending := make(chan pendingApproval, 4)
	executor.SetInteractions(forwardingInteractions{approve: func(ctx context.Context, source tool.AgentInteractionSource, request *perm.PermissionRequest, _ map[string]any) (bool, string) {
		if source.Workflow == nil || request.CallerAgent != source.Label() || !strings.Contains(request.CallerAgent, source.Workflow.NodeID) {
			t.Error("approval preview lost workflow/node attribution")
		}
		p := pendingApproval{source: source, reply: make(chan bool, 1), cancelled: make(chan struct{})}
		pending <- p
		select {
		case answer := <-p.reply:
			return answer, "independent decision"
		case <-ctx.Done():
			close(p.cancelled)
			return false, ctx.Err().Error()
		}
	}})
	wt := workflowtool.NewWorkflowTool()
	wt.SetExecutor(subagent.NewExecutorAdapter(executor))
	definition := "---\nname: review\n---\n```mermaid\nflowchart LR\n  first\n  second\n```\n\n## first\nCheck first\n\n## second\nCheck second\n"
	for _, parent := range []string{"parent-one", "parent-two"} {
		result := wt.Execute(core.WithToolCallID(context.Background(), parent), map[string]any{"definition": definition}, ".")
		if !result.Success {
			t.Fatal(result.Output)
		}
	}
	groups := make(map[string][]pendingApproval)
	agentIDs := make(map[string]bool)
	for range 4 {
		select {
		case p := <-pending:
			s := p.source
			if s.Workflow == nil || s.Workflow.TaskID == "" || s.Workflow.Name != "review" || s.Workflow.WorkerLabel != "" || s.TaskID != "" || s.ToolCallID != "worker-call" || s.AgentID == "" || agentIDs[s.AgentID] {
				t.Fatalf("wrong workflow attribution or worker address: %+v", s)
			}
			agentIDs[s.AgentID] = true
			if !strings.Contains(s.Label(), s.Workflow.TaskID) {
				t.Fatalf("missing run label: %q", s.Label())
			}
			groups[s.ParentToolCallID] = append(groups[s.ParentToolCallID], p)
		case <-time.After(5 * time.Second):
			t.Fatal("parallel approvals did not arrive")
		}
	}
	for _, parent := range []string{"parent-one", "parent-two"} {
		workers := groups[parent]
		if len(workers) != 2 {
			t.Fatalf("wrong spawning call correlation: %+v", groups)
		}
		if workers[0].source.Workflow.TaskID != workers[1].source.Workflow.TaskID || workers[0].source.Workflow.NodeID == workers[1].source.Workflow.NodeID {
			t.Fatal("nodes lost their shared run or distinct node identity")
		}
	}
	if groups["parent-one"][0].source.Workflow.TaskID == groups["parent-two"][0].source.Workflow.TaskID {
		t.Fatal("runs share task identity")
	}
	stopped, ok := task.Default().Get(groups["parent-one"][0].source.Workflow.TaskID)
	if !ok {
		t.Fatal("workflow task missing")
	}
	if err := stopped.Stop(); err != nil {
		t.Fatal(err)
	}
	for _, p := range groups["parent-one"] {
		select {
		case <-p.cancelled:
		case <-time.After(5 * time.Second):
			t.Fatal("stopped workflow approval not cancelled")
		}
	}
	// Opposite replies belong to distinct worker invocations, even after the
	// other workflow's cancellation has torn down its routing scope.
	groups["parent-two"][0].reply <- false
	groups["parent-two"][1].reply <- true
	running, ok := task.Default().Get(groups["parent-two"][0].source.Workflow.TaskID)
	if !ok || !running.WaitForCompletion(5*time.Second) {
		t.Fatal("surviving workflow did not finish")
	}
	if running.GetStatus().Status != task.StatusCompleted {
		t.Fatalf("surviving run corrupted: %+v", running.GetStatus())
	}
	if !stopped.WaitForCompletion(5 * time.Second) {
		t.Fatal("stopped workflow did not finish")
	}
	outcomes := make(map[string]bool)
	for _, call := range provider.Calls {
		for _, message := range call.Messages {
			for _, result := range message.ToolResults() {
				if result.ToolCallID == "worker-call" {
					outcomes[call.Messages[0].Text()] = !result.IsError
				}
			}
		}
	}
	if len(outcomes) != 2 || outcomes["Check "+groups["parent-two"][0].source.Workflow.NodeID] || !outcomes["Check "+groups["parent-two"][1].source.Workflow.NodeID] {
		t.Fatalf("approval decisions reached the wrong nodes: %v", outcomes)
	}
}
