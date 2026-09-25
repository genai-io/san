// Package workflow_test drives the Workflow tool through the real subagent
// executor. The tool's own tests stub that executor at tool.AgentExecutor,
// so what they cannot see is everything beneath it: that a rendered prompt
// reaches the model, that the turn's answer comes back as the node's output,
// and how a turn's ending is judged. Each test here asserts one of those.
// Graph semantics — omission, loops, the summary's wording — are owned by
// internal/workflow and internal/tool/workflow, not re-asserted here.
package workflow_test

import (
	"context"
	"iter"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"

	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/task"
	_ "github.com/genai-io/san/internal/tool/register"
	toolworkflow "github.com/genai-io/san/internal/tool/workflow"
)

// scriptedProvider answers by what it was asked rather than by call order:
// a workflow runs its nodes concurrently, so a queue would hand the wrong
// reply to whichever node reached the model first.
type scriptedProvider struct {
	replies []reply
	// stop, when set, ends every turn for that reason instead of a clean
	// end_turn. It must hold for every call, not only the first: San asks the
	// model to continue a truncated turn, and a clean answer to that
	// continuation would end the turn as a success.
	stop ai.StopReason

	mu   sync.Mutex
	seen []string
}

type reply struct{ whenPromptContains, answer string }

func (p *scriptedProvider) Client(string, map[string]string) (*ai.Client, error) {
	return aitest.Always(p.answer).Client(), nil
}

func (p *scriptedProvider) answer(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
	var prompt string
	for _, m := range req.Messages {
		if m.Role == ai.RoleUser {
			prompt = m.Content.Text()
		}
	}

	p.mu.Lock()
	p.seen = append(p.seen, prompt)
	p.mu.Unlock()

	if p.stop != "" {
		return aitest.Stops(p.stop, "half an ans")(ctx, req)
	}
	for _, r := range p.replies {
		if strings.Contains(prompt, r.whenPromptContains) {
			return aitest.Says(r.answer)(ctx, req)
		}
	}
	return aitest.Says("unscripted prompt: "+prompt)(ctx, req)
}

func (p *scriptedProvider) ListModels(context.Context) ([]llm.ModelInfo, error) { return nil, nil }
func (p *scriptedProvider) Name() string                                        { return "fake" }

// prompts returns every prompt the model was asked, in arrival order.
func (p *scriptedProvider) prompts() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.seen)
}

func (p *scriptedProvider) asked(substr string) bool {
	return slices.ContainsFunc(p.prompts(), func(got string) bool { return strings.Contains(got, substr) })
}

// run drives one definition end to end and returns the finished task.
func run(t *testing.T, provider llm.Provider, params map[string]any) task.TaskInfo {
	t.Helper()
	cwd := t.TempDir()
	executor := subagent.NewExecutor(provider, cwd, "fake-model", nil)
	wt := toolworkflow.NewWorkflowTool()
	wt.SetExecutor(subagent.NewExecutorAdapter(executor))

	if _, err := wt.PreparePermission(context.Background(), params, cwd); err != nil {
		t.Fatalf("PreparePermission: %v", err)
	}
	result := wt.ExecuteApproved(context.Background(), params, cwd)
	if !result.Success {
		t.Fatalf("Execute: %s", result.Error)
	}

	_, rest, _ := strings.Cut(result.Output, "Task ID: ")
	id, _, _ := strings.Cut(rest, "\n")
	bg, ok := task.Default().Get(id)
	if !ok {
		t.Fatalf("task %s was not registered", id)
	}
	if !bg.WaitForCompletion(30 * time.Second) {
		t.Fatalf("workflow did not finish:\n%s", bg.GetOutput())
	}
	return bg.GetStatus()
}

func TestWorkflow_SectioningThroughRealSubagents(t *testing.T) {
	const definition = "---\nname: review\nmax_parallel: 2\n---\n" +
		"```mermaid\nflowchart LR\n  diff --> sec & perf --> report\n```\n\n" +
		"## diff\nmode: explore\n\nSummarize {{input.base}}..HEAD\n\n" +
		"## sec\nmode: explore\n\nSecurity only: {{diff}}\n\n" +
		"## perf\nmode: explore\n\nPerformance only: {{diff}}\n\n" +
		"## report\nmode: explore\n\nMerge: {{sec}} and {{perf}}\n"

	provider := &scriptedProvider{replies: []reply{
		{"Summarize main..HEAD", "three packages changed"},
		{"Security only: three packages changed", "no reachable issues"},
		{"Performance only: three packages changed", "one extra allocation"},
		{"Merge: no reachable issues and one extra allocation", "ship it"},
	}}
	info := run(t, provider, map[string]any{
		"definition": definition,
		"inputs":     map[string]any{"base": "main"},
	})

	if info.Status != task.StatusCompleted {
		t.Fatalf("status = %s (%s)\n%s", info.Status, info.Error, info.Output)
	}
	if !strings.Contains(info.Output, "workflow review succeeded") {
		t.Fatalf("summary:\n%s", info.Output)
	}
	// The sink's output is the workflow's product, carried back to the
	// conversation that launched it.
	if !strings.Contains(info.Output, "## report\nship it") {
		t.Fatalf("summary lost the sink output:\n%s", info.Output)
	}
	// Four nodes, four turns: a node is one subagent turn, not a retry loop.
	if got := len(provider.prompts()); got != 4 {
		t.Fatalf("model was called %d times, want one per node:\n%q", got, provider.prompts())
	}
	if info.StepCount != 4 {
		t.Fatalf("step count = %d, want one per node", info.StepCount)
	}
}

func TestWorkflow_ForEachRunsOneSubagentPerPlanItem(t *testing.T) {
	const definition = "---\nname: split\n---\n" +
		"```mermaid\nflowchart LR\n  plan --> review --> merge\n```\n\n" +
		"## plan\nmode: explore\n\nSplit it, at most 4 tasks\n\n" +
		"## review\nfor_each: plan.tasks\nmax_workers: 4\nmode: explore\n\nReview {{item.prompt}}\n\n" +
		"## merge\nmode: explore\n\nMerge:\n{{review}}\n"

	provider := &scriptedProvider{replies: []reply{
		{"Split it", `{"tasks":[{"name":"llm","prompt":"error handling"},{"name":"tool","prompt":"permissions"}]}`},
		{"Review error handling", "llm looks fine"},
		{"Review permissions", "tool needs a guard"},
		{"Merge:", "two findings"},
	}}
	info := run(t, provider, map[string]any{"definition": definition})

	if info.Status != task.StatusCompleted {
		t.Fatalf("status = %s (%s)\n%s", info.Status, info.Error, info.Output)
	}
	// One turn for the plan, one per item, one to merge.
	if got := len(provider.prompts()); got != 4 {
		t.Fatalf("model was called %d times, want 4:\n%q", got, provider.prompts())
	}
	if !provider.asked("Merge:\n## llm\nllm looks fine\n\n## tool\ntool needs a guard") {
		t.Fatalf("merge did not receive the labelled worker outputs:\n%q", provider.prompts())
	}
}

func TestWorkflow_TruncatedTurnFailsItsNode(t *testing.T) {
	const definition = "---\nname: chain\n---\n" +
		"```mermaid\nflowchart LR\n  a --> b --> c\n```\n\n" +
		"## a\nmode: explore\n\nfirst\n\n" +
		"## b\nmode: explore\n\nsecond {{a}}\n\n" +
		"## c\nmode: explore\n\nthird {{b}}\n"

	// A turn that stops for any reason other than the model finishing is a
	// failed node — decided by the executor and the agent loop, beneath what
	// the tool's tests can reach. max_tokens is the cheapest one to stage.
	provider := &scriptedProvider{stop: ai.StopMaxTokens}
	info := run(t, provider, map[string]any{"definition": definition})

	if info.Status != task.StatusFailed {
		t.Fatalf("status = %s, want failed\n%s", info.Status, info.Output)
	}
	if !provider.asked("first") {
		t.Fatalf("node a never reached the model:\n%q", provider.prompts())
	}
	// Downstream of the failed node never reaches the model at all.
	if provider.asked("second") || provider.asked("third") {
		t.Fatalf("a node downstream of a failure reached the model:\n%q", provider.prompts())
	}
}
