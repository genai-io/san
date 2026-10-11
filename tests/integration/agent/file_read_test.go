package subagent_test

import (
	"context"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/broker"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/proc"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/tests/integration/testutil"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestAgentExploreReadsFilesWithReadDisabled(t *testing.T) {
	cwd := t.TempDir()
	const source = "package example // source inspected by the reviewer\n"
	if err := os.WriteFile(filepath.Join(cwd, "example.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	shell, err := proc.DefaultShell()
	if err != nil {
		t.Fatal(err)
	}
	input, recipe := `{"command":"cat example.go"}`, "cat path"
	if shell.Kind == proc.ShellPowerShell {
		input, recipe = `{"command":"Get-Content example.go"}`, "Get-Content path"
	}
	provider := &testutil.FakeProvider{Responses: []llm.CompletionResponse{
		testutil.ToolCallResponse(shell.Kind.ToolName(), "read-source", input),
		testutil.EndTurnResponse("Reviewed example.go"),
	}}
	executor := subagent.NewExecutor(provider, cwd, "fake-model", nil)
	executor.SetDisabledTools(map[string]bool{tool.ToolRead: true})
	result, err := executor.Run(context.Background(), tool.AgentExecRequest{Mode: "explore", Prompt: "Review example.go"})
	if err != nil || !result.Success {
		t.Fatalf("review failed: result=%+v err=%v", result, err)
	}
	if len(provider.Calls) != 2 {
		t.Fatalf("review used %d inferences, want 2", len(provider.Calls))
	}
	if prompt := provider.Calls[0].SystemPrompt; !strings.Contains(prompt, recipe) || !strings.Contains(prompt, "interpreters") {
		t.Fatalf("effective shell-only reading policy is missing:\n%s", prompt)
	}
	for _, message := range provider.Calls[1].Messages {
		for _, result := range message.ToolResults() {
			if !result.IsError && strings.Contains(result.Content.Text(), strings.TrimSpace(source)) {
				return
			}
		}
	}
	t.Fatal("source contents never reached the reviewer")
}

func TestAgentExploreReportsDeniedToolBeforeNextInferenceEnds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shell, err := proc.DefaultShell()
	if err != nil {
		t.Fatal(err)
	}
	notices := make(chan broker.Message, 1)
	broker.Register(broker.Main, func(message broker.Message) bool { notices <- message; return true })
	t.Cleanup(func() { broker.Unregister(broker.Main) })
	started := make(chan struct{})
	blocked := func(ctx context.Context, _ *ai.Request) iter.Seq2[ai.Delta, error] {
		return func(yield func(ai.Delta, error) bool) {
			close(started)
			<-ctx.Done()
			yield(ai.Delta{}, ctx.Err())
		}
	}
	driver := aitest.New(aitest.Asks(ai.ToolCall{
		ID: "denied-read", Name: shell.Kind.ToolName(), Input: `{"command":"python3 read.py"}`,
	}), blocked)
	executor := subagent.NewExecutor(driverProvider{&testutil.FakeProvider{}, driver}, t.TempDir(), "fake-model", nil)
	done := make(chan error, 1)
	go func() {
		_, err := executor.Run(ctx, tool.AgentExecRequest{TaskID: "review-1", Mode: "explore", Description: "Review source", Prompt: "Read the source"})
		done <- err
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("next inference did not start")
	}
	select {
	case notice := <-notices:
		if !strings.Contains(notice.Subject, "denied in Explore mode") || notice.Content != "" {
			t.Fatalf("permission denial was not reported directly: %+v", notice)
		}
	case <-ctx.Done():
		t.Fatal("permission denial stayed hidden behind inference")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not release the worker")
	}
}
