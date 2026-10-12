package subagent_test

import (
	"context"
	"iter"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/tests/integration/testutil"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

type backgroundRoutingProvider struct {
	*testutil.FakeProvider
	turn  func(string) aitest.Turn
	calls atomic.Int32
}

func (p *backgroundRoutingProvider) Client(model string, _ map[string]string) (*ai.Client, error) {
	p.calls.Add(1)
	return aitest.Always(p.turn(model)).Client(), nil
}

type backgroundRoutingResolver struct{ provider llm.Provider }

func (r backgroundRoutingResolver) Resolve(context.Context, llm.ProviderID) (llm.Provider, error) {
	return r.provider, nil
}

func TestConfiguredBackgroundModelsKeepRoutingAndCancellationSeparate(t *testing.T) {
	if err := task.Initialize(task.Options{OutputDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(task.ResetDefaultTracker)
	llm.RegisterProviderDisplay(llm.DeepSeek, llm.ProviderDisplay{Name: "DeepSeek"})
	type startedRun struct{ prompt, model string }
	started := make(chan startedRun, 3)
	release := make(chan struct{})
	workerProvider := &backgroundRoutingProvider{FakeProvider: &testutil.FakeProvider{}}
	workerProvider.turn = func(model string) aitest.Turn {
		return func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
			return func(yield func(ai.Delta, error) bool) {
				var prompt string
				for _, message := range req.Messages {
					if message.Role == ai.RoleUser {
						prompt += message.Content.Text()
					}
				}
				started <- startedRun{prompt, model}
				if !strings.Contains(prompt, "named-worker") {
					select {
					case <-ctx.Done():
						yield(ai.Delta{}, ctx.Err())
						return
					case <-release:
					}
				}
				for delta, err := range aitest.Says("done")(ctx, req) {
					if !yield(delta, err) {
						return
					}
				}
			}
		}
	}
	parent := &backgroundRoutingProvider{FakeProvider: &testutil.FakeProvider{}, turn: func(string) aitest.Turn { return aitest.Says("unexpected parent route") }}
	executor := subagent.NewExecutor(parent, t.TempDir(), "parent-model", nil)
	executor.SetDefaultModel("deepseek/configured-worker")
	executor.SetResolver(backgroundRoutingResolver{workerProvider})
	launch := func(req tool.AgentExecRequest) *task.AgentTask {
		t.Helper()
		worker, err := executor.RunBackground(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = worker.Stop(); worker.WaitForCompletion(time.Second) })
		return worker
	}
	stopped := launch(tool.AgentExecRequest{Prompt: "stop-worker"})
	finishing := launch(tool.AgentExecRequest{Prompt: "finish-worker"})
	for range 2 {
		select {
		case run := <-started:
			if run.model != "configured-worker" {
				t.Fatalf("background used wrong model: %+v", run)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("both workers did not reach the selected provider")
		}
	}
	if err := stopped.Stop(); err != nil {
		t.Fatal(err)
	}
	if !stopped.WaitForCompletion(5*time.Second) || stopped.GetStatus().Status != task.StatusStopped {
		t.Fatalf("worker did not stop: %+v", stopped.GetStatus())
	}
	if !finishing.IsRunning() {
		t.Fatalf("stopping one worker stopped its sibling: %+v", finishing.GetStatus())
	}
	close(release)
	if !finishing.WaitForCompletion(5*time.Second) || finishing.GetStatus().Status != task.StatusCompleted {
		t.Fatalf("sibling failed: %+v", finishing.GetStatus())
	}
	named := launch(tool.AgentExecRequest{Agent: "custom", Prompt: "named-worker",
		ResolvedAgentConfig: &subagent.AgentConfig{Name: "custom", Model: "deepseek/named-model"}})
	if !named.WaitForCompletion(5*time.Second) || named.GetStatus().Status != task.StatusCompleted {
		t.Fatalf("named worker failed: %+v", named.GetStatus())
	}
	select {
	case run := <-started:
		if run.model != "named-model" {
			t.Fatalf("default model overrode named configuration: %+v", run)
		}
	default:
		t.Fatal("named worker never reached the selected provider")
	}
	if parent.calls.Load() != 0 || workerProvider.calls.Load() != 3 {
		t.Fatalf("unexpected routes: parent=%d worker=%d", parent.calls.Load(), workerProvider.calls.Load())
	}
}
