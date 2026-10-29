package subagent

import (
	"context"
	"testing"

	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
)

func TestNewContinuationStateDropsInvocationCallbacks(t *testing.T) {
	run := &preparedRun{
		originalPrompt: "original", cfg: &runConfig{maxSteps: 12}, cwd: "/workspace",
		req: tool.AgentExecRequest{
			Agent: "worker", Prompt: "instruction", Model: "model", MaxSteps: 12,
			OnIdentity:  func(toolresult.AgentIdentity) {},
			OnActivity:  func(string) {},
			OnQuestion:  func(context.Context, *tool.QuestionRequest) (*tool.QuestionResponse, error) { return nil, nil },
			OnTextDelta: func(string) {}, OnTextEnd: func(error) {},
			OnToolStart: func(string, string) {}, OnToolResult: func(string, string, string, error) {},
		},
	}
	state := newContinuationState(run, "parent", "task")
	if state.req.OnIdentity != nil || state.req.OnActivity != nil || state.req.OnQuestion != nil ||
		state.req.OnTextDelta != nil || state.req.OnTextEnd != nil || state.req.OnToolStart != nil || state.req.OnToolResult != nil {
		t.Fatal("checkpoint retained invocation callbacks")
	}
	if run.req.OnIdentity == nil || run.req.OnActivity == nil || run.req.OnQuestion == nil ||
		run.req.OnTextDelta == nil || run.req.OnTextEnd == nil || run.req.OnToolStart == nil || run.req.OnToolResult == nil {
		t.Fatal("checkpoint construction mutated the active request")
	}
	if state.originalPrompt != run.originalPrompt || state.cfg != run.cfg || state.cwd != run.cwd || state.parent != "parent" || state.taskID != "task" ||
		state.req.Agent != run.req.Agent || state.req.Prompt != run.req.Prompt || state.req.Model != run.req.Model || state.req.MaxSteps != run.req.MaxSteps {
		t.Fatal("checkpoint lost continuation configuration")
	}
}
