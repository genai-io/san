package subagent_test

import (
	"context"
	"fmt"
	"iter"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/tool"
	agenttool "github.com/genai-io/san/internal/tool/agent"
	"github.com/genai-io/san/tests/integration/testutil"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

type driverProvider struct {
	*testutil.FakeProvider
	driver *aitest.Driver
}

func (p driverProvider) Client(string, map[string]string) (*ai.Client, error) {
	return p.driver.Client(), nil
}

func TestForegroundAgentsRunInParallelAndJoinBeforeParentInference(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started, release, completed := make(chan struct{}, 4), make(chan struct{}, 4), make(chan struct{}, 4)
	workers := aitest.Always(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
		return func(yield func(ai.Delta, error) bool) {
			started <- struct{}{}
			select {
			case <-ctx.Done():
				yield(ai.Delta{}, ctx.Err())
				return
			case <-release:
			}
			for delta, err := range aitest.Says("Review completed")(ctx, req) {
				if !yield(delta, err) {
					return
				}
			}
		}
	})
	executor := subagent.NewExecutor(driverProvider{&testutil.FakeProvider{}, workers}, t.TempDir(), "fake-model", nil)
	executor.SetDisabledTools(map[string]bool{tool.ToolRead: true})
	spawn := agenttool.NewAgentTool()
	spawn.SetExecutor(subagent.NewExecutorAdapter(executor))
	calls := make([]ai.ToolCall, 4)
	for i := range calls {
		calls[i] = ai.ToolCall{
			ID: fmt.Sprintf("review-%d", i), Name: tool.ToolAgent,
			Input: fmt.Sprintf(`{"description":"Review changes","prompt":"review-%d","mode":"explore","run_in_background":false}`, i),
		}
	}
	model := aitest.New(aitest.Asks(calls...), aitest.Says("All reviews received"))
	parent := core.NewAgent(core.Config{
		Client: func([]core.Message) (*ai.Client, error) { return model.Client(), nil },
		System: core.NewSystem(), Tools: core.NewTools(tool.AdaptTool(spawn, spawn.Schema(), func() string { return "." })),
		OutboxBuf: -1,
		OnEvent: func(event core.Event) {
			if _, ok := event.(sdkagent.ToolEnd); ok {
				completed <- struct{}{}
			}
		},
	})
	parent.Append(core.UserMessage("Review changed code", nil))
	done := make(chan error, 1)
	go func() { _, err := parent.ThinkAct(ctx); done <- err }()
	await := func(ch <-chan struct{}, what string) {
		t.Helper()
		select {
		case <-ch:
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s", what)
		}
	}
	for range 4 {
		await(started, "all four concurrent reviewers to start")
	}
	for range 3 {
		release <- struct{}{}
	}
	for range 3 {
		await(completed, "three reviewers to finish")
	}
	if n := model.Calls(); n != 1 {
		t.Fatalf("parent inferred %d times before the final result, want 1", n)
	}
	release <- struct{}{}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("parent did not resume after all four reviews finished")
	}
	if n := model.Calls(); n != 2 {
		t.Fatalf("parent inferred %d times, want launch + collect only", n)
	}
	results := 0
	for _, message := range parent.Messages() {
		for _, result := range message.ToolResults() {
			results++
			if result.IsError {
				t.Fatalf("review failed: %s", result.Content.Text())
			}
		}
	}
	if results != 4 {
		t.Fatalf("parent received %d results, want 4", results)
	}
}
