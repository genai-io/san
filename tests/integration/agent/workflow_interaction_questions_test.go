package subagent_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	workflowtool "github.com/genai-io/san/internal/tool/workflow"
	"github.com/genai-io/san/tests/integration/testutil"
)

func TestWorkflowQuestionsKeepNodeOriginAndAnswers(t *testing.T) {
	if err := task.Initialize(task.Options{OutputDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(task.ResetDefaultTracker)
	bridge := conv.NewAgentToUI(4)
	defer bridge.Close()
	question := testutil.ToolCallResponse(tool.ToolAskUserQuestion, "question-call", `{"question":"Choose an answer","options":["first","second"]}`)
	provider := &testutil.FakeProvider{Responses: []llm.CompletionResponse{
		question, question, testutil.EndTurnResponse("done"), testutil.EndTurnResponse("done"),
	}}
	executor := subagent.NewExecutor(provider, t.TempDir(), "fake-model", nil)
	executor.SetInteractions(bridge)
	wt := workflowtool.NewWorkflowTool()
	wt.SetExecutor(subagent.NewExecutorAdapter(executor))
	definition := "---\nname: questions\n---\n```mermaid\nflowchart LR\n  first\n  second\n```\n\n## first\nAsk first\n\n## second\nAsk second\n"
	result := wt.Execute(core.WithToolCallID(context.Background(), "workflow-call"), map[string]any{"definition": definition}, ".")
	if !result.Success {
		t.Fatal(result.Output)
	}
	_, rest, _ := strings.Cut(result.Output, "Task ID: ")
	runID, _, _ := strings.Cut(rest, "\n")
	bg, ok := task.Default().Get(runID)
	if !ok {
		t.Fatal("workflow task missing")
	}
	t.Cleanup(func() {
		_ = bg.Stop()
		if !bg.WaitForCompletion(5 * time.Second) {
			t.Error("workflow questions did not stop during cleanup")
		}
	})
	requests := make(map[string]*conv.InteractionRequest)
	for range 2 {
		req := pollWorkerInteraction(t, bridge)
		source := req.Source
		if req.Question == nil || source.Workflow == nil || source.Workflow.Name != "questions" || source.Workflow.TaskID != runID || source.ParentToolCallID != "workflow-call" || source.ToolCallID != "question-call" || source.TaskID == "" || source.TaskID == source.Workflow.TaskID || source.AgentID == "" {
			t.Fatalf("question lost workflow/node provenance: %+v", source)
		}
		if !strings.Contains(source.Label(), source.Workflow.NodeID) || !strings.Contains(source.Label(), runID) {
			t.Fatalf("question header lost node/run identity: %q", source.Label())
		}
		requests[source.Workflow.NodeID] = req
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
		t.Fatal("answered workflow did not complete")
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
