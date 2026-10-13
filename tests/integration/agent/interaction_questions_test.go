package subagent_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/tool"
	_ "github.com/genai-io/san/internal/tool/ask"
	"github.com/genai-io/san/tests/integration/testutil"
)

func workerQuestionProvider() *testutil.FakeProvider {
	return &testutil.FakeProvider{Responses: []llm.CompletionResponse{
		testutil.ToolCallResponse(tool.ToolAskUserQuestion, "question-call", `{"question":"Choose an answer","options":["first","second"]}`),
		testutil.EndTurnResponse("finished"),
	}}
}

func pollWorkerInteraction(t *testing.T, bridge *conv.AgentToUI) *conv.InteractionRequest {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if msg, ok := bridge.Check()().(conv.InteractionRequestMsg); ok {
			return msg.Request
		}
	}
	t.Fatal("worker interaction did not reach transport")
	return nil
}

func TestBackgroundQuestionCancellationIsIndependent(t *testing.T) {
	bridge := conv.NewAgentToUI(4)
	defer bridge.Close()
	providers := []*testutil.FakeProvider{workerQuestionProvider(), workerQuestionProvider()}
	first := subagent.NewExecutor(providers[0], t.TempDir(), "fake-model", nil)
	second := subagent.NewExecutor(providers[1], t.TempDir(), "fake-model", nil)
	for _, executor := range []*subagent.Executor{first, second} {
		executor.SetParentPermissionMode(func() subagent.PermissionMode { return subagent.PermissionBypass })
		executor.SetInteractions(bridge)
	}
	a, err := first.RunBackground(tool.AgentExecRequest{Prompt: "ask", ParentToolCallID: "parent-a"})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Stop()
	b, err := second.RunBackground(tool.AgentExecRequest{Prompt: "ask", ParentToolCallID: "parent-b"})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Stop()
	requests := map[string]*conv.InteractionRequest{}
	for range 2 {
		req := pollWorkerInteraction(t, bridge)
		if req.Question == nil || req.Source.AgentID == "" || req.Source.ToolCallID != "question-call" || req.Source.PermissionMode != string(subagent.PermissionBypass) {
			t.Fatalf("incorrect question source: %+v", req.Source)
		}
		requests[req.Source.TaskID] = req
	}
	if len(requests) != 2 || requests[a.GetID()] == nil || requests[b.GetID()] == nil {
		t.Fatal("workers lost their independent task identities")
	}
	if requests[a.GetID()].Question.Request.ID == requests[b.GetID()].Question.Request.ID {
		t.Fatal("concurrent questions reused a request ID")
	}
	a.Stop()
	if !a.WaitForCompletion(3 * time.Second) {
		t.Fatal("canceled background question remained blocked")
	}
	if requests[a.GetID()].Context.Err() == nil || requests[b.GetID()].Context.Err() != nil || !b.IsRunning() {
		t.Fatal("canceling one worker affected its sibling")
	}
	question := requests[b.GetID()].Question
	question.Reply <- &tool.QuestionResponse{RequestID: question.Request.ID, Answers: map[int][]string{0: {"second"}}}
	if !b.WaitForCompletion(3 * time.Second) {
		t.Fatal("answered worker did not resume")
	}
	found := false
	for _, message := range providers[1].Calls[1].Messages {
		for _, result := range message.ToolResults() {
			if result.ToolCallID == "question-call" && !result.IsError && strings.Contains(result.Content.Text(), "second") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("answer did not reach the exact suspended worker")
	}
}

func TestHeadlessAndClosedUIQuestionsFailPromptly(t *testing.T) {
	for _, closed := range []bool{false, true} {
		t.Run(map[bool]string{false: "headless", true: "closed"}[closed], func(t *testing.T) {
			provider := workerQuestionProvider()
			executor := subagent.NewExecutor(provider, t.TempDir(), "fake-model", nil)
			if closed {
				bridge := conv.NewAgentToUI(1)
				bridge.Close()
				executor.SetInteractions(bridge)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if _, err := executor.Run(ctx, tool.AgentExecRequest{Prompt: "ask"}); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, message := range provider.Calls[1].Messages {
				for _, result := range message.ToolResults() {
					if result.ToolCallID == "question-call" && result.IsError {
						found = true
					}
				}
			}
			if !found || ctx.Err() != nil {
				t.Fatal("unavailable UI invented an answer or left the worker waiting")
			}
		})
	}
}
