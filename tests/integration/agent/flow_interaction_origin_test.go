package subagent_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/app/ask"
	"github.com/genai-io/san/internal/flow"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/tests/integration/testutil"
)

// Each worker reaches the real executor's approval bridge before any reply is
// released. Cancelling one run must leave the other run's replies independent.
func TestFlowInteractionOriginAndRoutingIsolation(t *testing.T) {
	if err := task.Initialize(task.Options{OutputDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, bg := range task.Default().ListRunning() {
			_ = bg.Stop()
			if !bg.WaitForCompletion(5 * time.Second) {
				t.Errorf("flow %s did not stop during cleanup", bg.GetID())
			}
		}
		task.ResetDefaultTracker()
	})
	responses := make([]llm.CompletionResponse, 0, 8)
	for range 4 {
		responses = append(responses, testutil.ToolCallResponse(tool.ToolBash, "worker-call", `{"command":"touch approved-marker","description":"test flow approval","execution_mode":"foreground"}`))
	}
	for range 4 {
		responses = append(responses, testutil.EndTurnResponse("done"))
	}
	provider := &testutil.FakeProvider{Responses: responses}
	executor := newIntegrationExecutor(t, provider, t.TempDir(), "fake-model", nil)
	type pendingApproval struct {
		source    tool.AgentInteractionSource
		reply     chan bool
		cancelled chan struct{}
	}
	pending := make(chan pendingApproval, 4)
	configureIntegrationApprovals(t, executor, func(ctx context.Context, source tool.AgentInteractionSource, request *perm.PermissionRequest, _ map[string]any) ask.PermissionDecision {
		if source.Flow == nil || request.CallerAgent != source.Label() || !strings.Contains(request.CallerAgent, source.Flow.NodeID) {
			t.Error("approval preview lost flow/node attribution")
		}
		p := pendingApproval{source: source, reply: make(chan bool, 1), cancelled: make(chan struct{})}
		pending <- p
		select {
		case answer := <-p.reply:
			return ask.PermissionDecision{Approved: answer, Request: request}
		case <-ctx.Done():
			close(p.cancelled)
			return ask.PermissionDecision{Request: request}
		}
	})
	flows := flow.NewService(task.Default())
	flows.SetExecutor(subagent.NewExecutorAdapter(executor))
	definition := "---\nname: review\n---\n```mermaid\nflowchart LR\n  first\n  second\n```\n\n## first\nCheck first\n\n## second\nCheck second\n"
	var runIDs []string
	for range 2 {
		runIDs = append(runIDs, testutil.LaunchFlow(t, flows, definition, nil))
	}
	groups := make(map[string][]pendingApproval)
	agentIDs := make(map[string]bool)
	for range 4 {
		select {
		case p := <-pending:
			s := p.source
			if s.Flow == nil || s.Flow.TaskID == "" || s.Flow.Name != "review" || s.Flow.WorkerLabel != "" || s.TaskID == "" || s.TaskID == s.Flow.TaskID || s.ToolCallID != "worker-call" || s.AgentID == "" || agentIDs[s.AgentID] {
				t.Fatalf("wrong flow attribution or worker address: %+v", s)
			}
			agentIDs[s.AgentID] = true
			if !strings.Contains(s.Label(), s.Flow.TaskID) {
				t.Fatalf("missing run label: %q", s.Label())
			}
			if s.ParentToolCallID != "" {
				t.Fatal("direct Flow service invented a spawning tool call")
			}
			groups[s.Flow.TaskID] = append(groups[s.Flow.TaskID], p)
		case <-time.After(5 * time.Second):
			t.Fatal("parallel approvals did not arrive")
		}
	}
	for _, parent := range runIDs {
		workers := groups[parent]
		if len(workers) != 2 {
			t.Fatalf("wrong spawning call correlation: %+v", groups)
		}
		if workers[0].source.Flow.TaskID != workers[1].source.Flow.TaskID || workers[0].source.Flow.NodeID == workers[1].source.Flow.NodeID {
			t.Fatal("nodes lost their shared run or distinct node identity")
		}
	}
	if groups[runIDs[0]][0].source.Flow.TaskID == groups[runIDs[1]][0].source.Flow.TaskID {
		t.Fatal("runs share task identity")
	}
	stopped, ok := task.Default().Get(groups[runIDs[0]][0].source.Flow.TaskID)
	if !ok {
		t.Fatal("flow task missing")
	}
	if err := stopped.Stop(); err != nil {
		t.Fatal(err)
	}
	for _, p := range groups[runIDs[0]] {
		select {
		case <-p.cancelled:
		case <-time.After(5 * time.Second):
			t.Fatal("stopped flow approval not cancelled")
		}
	}
	// Opposite replies belong to distinct worker invocations, even after the
	// other flow's cancellation has torn down its routing scope.
	groups[runIDs[1]][0].reply <- false
	groups[runIDs[1]][1].reply <- true
	running, ok := task.Default().Get(groups[runIDs[1]][0].source.Flow.TaskID)
	if !ok || !running.WaitForCompletion(5*time.Second) {
		t.Fatal("surviving flow did not finish")
	}
	if running.GetStatus().Status != task.StatusCompleted {
		t.Fatalf("surviving run corrupted: %+v", running.GetStatus())
	}
	if !stopped.WaitForCompletion(5 * time.Second) {
		t.Fatal("stopped flow did not finish")
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
	if len(outcomes) != 2 || outcomes["Check "+groups[runIDs[1]][0].source.Flow.NodeID] || !outcomes["Check "+groups[runIDs[1]][1].source.Flow.NodeID] {
		t.Fatalf("approval decisions reached the wrong nodes: %v", outcomes)
	}
}
