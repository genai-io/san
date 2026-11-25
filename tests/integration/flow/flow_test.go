// Package flow_test drives the Flow service through the real subagent
// executor. The service's unit tests stub that executor at tool.AgentExecutor,
// so what they cannot see is everything beneath it: that a rendered prompt
// reaches the model, that the turn's answer comes back as the node's output,
// and how a turn's ending is judged. Each test here asserts one of those.
// Graph semantics — omission, loops, the summary's wording — are owned by
// internal/flow, not re-asserted here.
package flow_test

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

	"github.com/genai-io/san/internal/flow"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/task"
	_ "github.com/genai-io/san/internal/tool/fs" // register Read as a real host does
	"github.com/genai-io/san/tests/integration/testutil"
)

// scriptedProvider answers by what it was asked rather than by call order:
// a flow runs its nodes concurrently, so a queue would hand the wrong
// reply to whichever node reached the model first.
type scriptedProvider struct {
	replies []reply
	observe func(*ai.Request) // optional observer of the actual model-facing catalogue
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
	if p.observe != nil {
		p.observe(req)
	}
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
	store := testutil.OpenSessionStore(t, cwd)
	parent, err := store.Create(t.Context(), session.CreateOptions{AgentID: "main", InitialCWD: cwd})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Close() })
	executor := subagent.NewExecutor(provider, cwd, "fake-model", nil)
	if err := executor.SetSessions(store, parent); err != nil {
		t.Fatal(err)
	}
	executor.ConfigureOwner(task.Default(), nil, nil, nil)
	wt := flow.NewService(task.Default())
	wt.SetExecutor(subagent.NewExecutorAdapter(executor))
	inputs := map[string]string{}
	if values, ok := params["inputs"].(map[string]any); ok {
		for key, value := range values {
			inputs[key] = value.(string)
		}
	}
	id := testutil.LaunchFlow(t, wt, params["definition"].(string), inputs)
	bg, ok := task.Default().Get(id)
	if !ok {
		t.Fatalf("task %s was not registered", id)
	}
	if !bg.WaitForCompletion(30 * time.Second) {
		t.Fatalf("flow did not finish:\n%s", bg.GetOutput())
	}
	return bg.GetStatus()
}

func TestFlow_SectioningThroughRealSubagents(t *testing.T) {
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
	if info.Output != "ship it" {
		t.Fatalf("final sink answer:\n%s", info.Output)
	}
	if strings.Contains(info.LiveView, "ship it") || strings.Contains(info.LiveView, "Activity") || !strings.Contains(info.LiveView, "report") {
		t.Fatalf("parent must show only its graph:\n%s", info.LiveView)
	}
	children := nativeChildren(t, info, 4)
	if !slices.ContainsFunc(children, func(child task.TaskInfo) bool { return strings.Contains(child.Output, "ship it") }) {
		t.Fatalf("native children lost the sink output: %+v", children)
	}
	// Four nodes, four turns: a node is one subagent turn, not a retry loop.
	if got := len(provider.prompts()); got != 4 {
		t.Fatalf("model was called %d times, want one per node:\n%q", got, provider.prompts())
	}
	if info.StepCount != 4 {
		t.Fatalf("step count = %d, want one per node", info.StepCount)
	}
}

func TestFlow_ForEachRunsOneSubagentPerPlanItem(t *testing.T) {
	const definition = "---\nname: split\n---\n" +
		"```mermaid\nflowchart LR\n  plan --> review --> merge\n```\n\n" +
		"## plan\nmode: explore\ntools: []\n\nSplit it, at most 4 tasks\n\n" +
		"## review\nfor_each: plan.tasks\nmax_workers: 4\nmode: explore\ntools: [Read]\n\nReview {{item.prompt}}\n\n" +
		"## merge\nmode: explore\ntools: []\n\nMerge:\n{{review}}\n"

	provider := &scriptedProvider{replies: []reply{
		{"Split it", `{"tasks":[{"name":"llm","prompt":"error handling"},{"name":"tool","prompt":"permissions"}]}`},
		{"Review error handling", "llm looks fine"},
		{"Review permissions", "tool needs a guard"},
		{"Merge:", "two findings"},
	}}
	provider.observe = func(req *ai.Request) {
		var want, offered []string
		for _, msg := range req.Messages {
			if msg.Role == ai.RoleUser && strings.Contains(msg.Content.Text(), "Review ") {
				want = []string{"Read"}
			}
		}
		for _, schema := range req.Tools {
			offered = append(offered, schema.Name)
		}
		if !slices.Equal(offered, want) {
			t.Errorf("model-facing Flow tools = %v; want %v", offered, want)
		}
	}
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

func TestFlow_TruncatedTurnFailsItsNode(t *testing.T) {
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

func TestFlow_RetryDiscardsFailedStreamText(t *testing.T) {
	provider := &testutil.FakeProvider{Responses: []llm.CompletionResponse{
		{Content: ai.TextContent(strings.Repeat("discarded draft ", 16)), Err: &ai.Error{Kind: ai.KindNetwork, Message: "stream stalled"}},
		{Content: ai.TextContent("accepted reply"), StopReason: ai.StopEndTurn},
	}}
	const definition = "```mermaid\nflowchart LR\n  a\n```\n\n## a\nmode: explore\n\nCheck a\n"
	info := run(t, provider, map[string]any{"definition": definition})
	if info.Status != task.StatusCompleted || len(provider.Calls) != 2 || !strings.Contains(info.Output, "accepted reply") {
		t.Fatalf("retry did not succeed: calls=%d status=%s error=%s output=%s", len(provider.Calls), info.Status, info.Error, info.Output)
	}
	if strings.Contains(info.LiveView, "discarded draft") || strings.Contains(info.LiveView, "accepted reply") || strings.Contains(info.LiveView, "Activity") {
		t.Fatalf("stream text was copied into the parent graph:\n%s", info.LiveView)
	}
	child := nativeChildren(t, info, 1)[0]
	if strings.Contains(child.Output, "discarded draft") || !strings.Contains(child.Output, "accepted reply") {
		t.Fatalf("native child did not discard failed stream text: %+v", child)
	}
}

// Native tasks remain the execution and output owners; the Flow task is a scheduler.
func nativeChildren(t *testing.T, parent task.TaskInfo, want int) []task.TaskInfo {
	t.Helper()
	var children []task.TaskInfo
	for _, bg := range task.Default().List() {
		child := bg.GetStatus()
		if child.ParentTaskID == parent.ID {
			if child.ID == "" || child.ID == parent.ID {
				t.Fatalf("native child lost its distinct task identity: %+v", child)
			}
			children = append(children, child)
		}
	}
	if len(children) != want {
		t.Fatalf("parent %s has %d native children, want %d", parent.ID, len(children), want)
	}
	return children
}
