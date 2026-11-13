package subagent_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/hook"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/tool"
	agenttool "github.com/genai-io/san/internal/tool/agent"
	"github.com/genai-io/san/tests/integration/testutil"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestAgent_ResultSurvivesPersistenceAndRendering(t *testing.T) {
	store, rec := integrationSessions(t)
	provider := &testutil.FakeProvider{Responses: []llm.CompletionResponse{
		{Content: ai.TextContent("Verified finding"), StopReason: ai.StopEndTurn, Usage: llm.Usage{Input: 100, Output: 25}},
	}}
	executor := subagent.NewExecutor(provider, t.TempDir(), "worker-model", nil)
	bindIntegrationSessions(t, executor, store, rec)
	agentTool := agenttool.NewAgentTool()
	agentTool.SetExecutor(subagent.NewExecutorAdapter(executor))
	adapted := tool.AdaptTool(agentTool, core.ToolSchema{Name: "Agent"}, func() string { return "." })
	owner := core.Message{ID: "call-1", Role: ai.RoleAssistant, Content: ai.Content{{Type: ai.BlockToolCall, ToolCall: &ai.ToolCall{
		ID: "worker-1", Name: "Agent", Input: `{"prompt":"Investigate"}`,
	}}}}
	scope := rec.Completion(session.PurposeBtw)
	callProvider := &testutil.FakeProvider{Responses: []llm.CompletionResponse{{Content: owner.Content, StopReason: ai.StopToolUse}}}
	if _, err := llm.Complete(context.Background(), callProvider, llm.CompletionOptions{Model: "m", Messages: []core.Message{core.UserMessage("Investigate", nil)}, Admission: scope.Admission()}); err != nil {
		t.Fatal(err)
	}
	if err := scope.FinishMessage(context.Background(), owner); err != nil {
		t.Fatal(err)
	}
	if err := rec.Append(context.Background(), session.Event{Payload: session.PermissionDecidePayload{CallID: "worker-1", Decision: "allow", By: session.ByConfig}}); err != nil {
		t.Fatal(err)
	}
	result, runErr := adapted.Run(context.Background(), ai.ToolCall{ID: "worker-1", Name: "Agent", Input: `{"prompt":"Investigate"}`})
	if runErr != nil {
		t.Fatal(runErr)
	}
	defer tool.PopSideEffect("worker-1")
	text := sdkagent.ResultText(result, runErr)
	if strings.Contains(text, "Tokens:") || !strings.Contains(text, "Transcript:") {
		t.Fatalf("model result must contain findings and inspection pointers: %q", text)
	}
	msg := core.ToolResultMessage(core.ToolResult{ToolCallID: "worker-1", ToolName: "Agent", Content: ai.TextContent(text)})
	msg.ID = "result-1"
	details, err := json.Marshal(result.Details)
	if err != nil {
		t.Fatal(err)
	}
	if err := rec.Append(context.Background(), session.Event{Payload: session.MessageAppendPayload{Messages: []core.Message{msg}}}, session.Event{Payload: session.ToolFinishPayload{CallID: "worker-1", Outcome: session.OutcomeSuccess, Details: details}}); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Read(context.Background(), rec.ID())
	if err != nil {
		t.Fatal(err)
	}
	if len(store.ChatRows(loaded)) != 2 || store.ChatRows(loaded)[1].ToolDetails == nil {
		t.Fatalf("agent details lost on resume: %+v", store.ChatRows(loaded))
	}
	// Rendering receives the resolved preference; zero explicitly hides previews.
	data := conv.ToolResultData{ToolName: "Agent", Content: store.ChatRows(loaded)[1].ToolResult.Content.Text(), Details: store.ChatRows(loaded)[1].ToolDetails, Width: 100, AgentPreviewLines: setting.NewData().AgentPreviewLineCount()}
	collapsed := ansi.Strip(conv.RenderToolResultInline(data, nil))
	if !strings.Contains(collapsed, "Verified finding") {
		t.Fatalf("resumed UI lost its result: %q", collapsed)
	}
	for _, metadata := range []string{"125 tokens", "worker-model", "Transcript:", "AgentID:", "Finished"} {
		if strings.Contains(collapsed, metadata) {
			t.Fatalf("default result leaked body/metadata %q: %q", metadata, collapsed)
		}
	}
	data.Expanded = true
	expanded := ansi.Strip(conv.RenderToolResultInline(data, nil))
	for _, want := range []string{"Verified finding", "Transcript:", "125 tokens", "worker-model"} {
		if !strings.Contains(expanded, want) {
			t.Fatalf("expanded resumed UI lost %q: %q", want, expanded)
		}
	}
}

func integrationSessions(t *testing.T) (*session.Store, *session.Session) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	store, err := session.OpenStore(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	opened, err := store.Create(context.Background(), session.CreateOptions{ID: "parent-1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	return store, opened
}

func bindIntegrationSessions(t *testing.T, executor *subagent.Executor, store *session.Store, parent *session.Session) {
	t.Helper()
	if err := executor.SetSessions(store, parent); err != nil {
		t.Fatal(err)
	}
}

func newIntegrationExecutor(t *testing.T, provider llm.Provider, cwd, model string, hooks hook.Handler) *subagent.Executor {
	t.Helper()
	store, parent := integrationSessions(t)
	executor := subagent.NewExecutor(provider, cwd, model, hooks)
	bindIntegrationSessions(t, executor, store, parent)
	return executor
}
