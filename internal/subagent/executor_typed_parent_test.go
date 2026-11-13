package subagent

import (
	"context"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/internal/tool/toolresult"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

type typedChildTool struct{ executor *Executor }

func (typedChildTool) Schema() ai.Schema {
	return ai.Schema{Name: "Agent", Definition: map[string]any{"type": "object"}}
}
func (t typedChildTool) Run(ctx context.Context, call ai.ToolCall) (sdkagent.Result, error) {
	child, err := t.executor.Run(core.WithToolCallID(ctx, call.ID), tool.AgentExecRequest{Prompt: "child work"})
	if err != nil {
		return sdkagent.Result{}, err
	}
	result := sdkagent.TextResult(child.Content)
	result.Details = toolresult.AgentDetails{Identity: child.Identity}
	return result, nil
}

func TestTypedParentRecorderAcceptsChildLink(t *testing.T) {
	ctx := context.Background()
	cwd := t.TempDir()
	store, err := session.OpenStore(ctx, cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	parent, err := store.Create(ctx, session.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	e := NewExecutor(continuationProvider{aitest.New(aitest.Says("child answer"))}, cwd, "aitest", nil)
	if err := e.SetTypedSessions(store, parent); err != nil {
		t.Fatal(err)
	}
	driver := aitest.New(aitest.Asks(ai.ToolCall{ID: "launch", Name: "Agent", Input: "{}"}), aitest.Says("accepted"))
	recorder := session.NewRecorder(parent, "aitest")
	tools := core.NewTools()
	tools.Add(typedChildTool{e}, "test")
	ag := core.NewAgent(core.Config{Client: func([]core.Message) (*ai.Client, error) { return driver.Client(), nil }, System: core.NewSystem(), Tools: tools, OutboxBuf: -1, InferenceAdmission: recorder.InferenceAdmission, ToolAdmission: recorder.ToolAdmission, OutputAdmission: recorder.OutputAdmission, OnEvent: recorder.RecordEvent, Gate: func(ctx context.Context, call sdkagent.PreToolContext) (sdkagent.Decision, error) {
		return sdkagent.Decision{}, recorder.RecordPermission(ctx, perm.Resolution{CallID: call.Call.ID, Decision: "allow", By: "config"})
	}})
	ag.Append(core.UserMessage("delegate", nil))
	if _, err := ag.ThinkAct(ctx); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Err(); err != nil {
		t.Fatal(err)
	}
	snapshot := parent.Snapshot()
	linked := ""
	resultFound := false
	for _, event := range snapshot.Events() {
		if finish, ok := event.Payload.(*session.ToolFinishPayload); ok && finish.CallID == "launch" {
			linked = finish.ChildSession
		}
		if append, ok := event.Payload.(*session.MessageAppendPayload); ok {
			for _, message := range append.Messages {
				for _, result := range message.ToolResults() {
					if result.ToolCallID == "launch" {
						resultFound = true
					}
				}
			}
		}
	}
	if linked == "" || !resultFound {
		t.Fatalf("missing accepted child result/link: %s %v", linked, resultFound)
	}
	child, err := store.Open(ctx, linked)
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	snapshot = child.Snapshot()
	link := snapshot.Identity().Parent
	if link == nil || link.Session != parent.ID() || link.CallID != "launch" {
		t.Fatalf("wrong original link: %+v", link)
	}
}
