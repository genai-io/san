package subagent_test

import (
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/app/tui/chat"
	"github.com/genai-io/san/internal/flow"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/tests/integration/testutil"
)

func TestFlowQuestionsKeepNodeOriginAndAnswers(t *testing.T) {
	if err := task.Initialize(task.Options{OutputDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(task.ResetDefault)
	bridge := chat.NewAgentToUI(4)
	defer bridge.Close()
	question := testutil.ToolCallResponse(tool.ToolAskUserQuestion, "question-call", `{"question":"Choose an answer","options":["first","second"]}`)
	provider := &testutil.FakeProvider{Responses: []llm.CompletionResponse{
		question, question, testutil.EndTurnResponse("done"), testutil.EndTurnResponse("done"),
	}}
	executor := newIntegrationExecutor(t, provider, t.TempDir(), "fake-model", nil)
	executor.SetInteractions(bridge)
	flows := flow.NewService(task.Default())
	flows.SetExecutor(subagent.NewExecutorAdapter(executor))
	definition := "---\nname: questions\n---\n```mermaid\nflowchart LR\n  first\n  second\n```\n\n## first\nAsk first\n\n## second\nAsk second\n"
	runID := testutil.LaunchFlow(t, flows, definition, nil)
	bg, ok := task.Default().Get(runID)
	if !ok {
		t.Fatal("flow task missing")
	}
	t.Cleanup(func() {
		_ = bg.Stop()
		if !bg.WaitForCompletion(5 * time.Second) {
			t.Error("flow questions did not stop during cleanup")
		}
	})
	requests := make(map[string]*chat.UserPromptRequest)
	for range 2 {
		req := pollWorkerInteraction(t, bridge)
		source := req.Source
		if req.Question == nil || source.Flow == nil || source.Flow.Name != "questions" || source.Flow.TaskID != runID || source.ParentToolCallID != "" || source.ToolCallID != "question-call" || source.TaskID == "" || source.TaskID == source.Flow.TaskID || source.AgentID == "" {
			t.Fatalf("question lost flow/node provenance: %+v", source)
		}
		if !strings.Contains(source.Label(), source.Flow.NodeID) || !strings.Contains(source.Label(), runID) {
			t.Fatalf("question header lost node/run identity: %q", source.Label())
		}
		requests[source.Flow.NodeID] = req
	}
	first, second := requests["first"], requests["second"]
	if first == nil || second == nil || first.Source.AgentID == second.Source.AgentID || first.Question.Request.ID == second.Question.Request.ID {
		t.Fatal("parallel questions share a worker or request identity")
	}
	// Answer in reverse node order; each answer must return to its own inference.
	for _, node := range []string{"second", "first"} {
		req := requests[node].Question
		req.Reply <- &tool.QuestionResponse{RequestID: req.Request.ID, Answers: map[int][]string{0: {node}}}
	}
	if !bg.WaitForCompletion(5*time.Second) || bg.GetStatus().Status != task.StatusCompleted {
		t.Fatal("answered flow did not complete")
	}
	answers := make(map[string]string)
	for _, call := range provider.Calls {
		for _, message := range call.Messages {
			for _, result := range message.ToolResults() {
				if result.ToolCallID == "question-call" && !result.IsError {
					answers[call.Messages[0].Text()] = result.Content.Text()
				}
			}
		}
	}
	for _, node := range []string{"first", "second"} {
		if !strings.Contains(answers["Ask "+node], "Choose an answer → "+node) {
			t.Fatalf("answer reached the wrong node: %v", answers)
		}
	}
}
