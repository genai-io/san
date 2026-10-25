package subagent_test

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/tool"
	agenttool "github.com/genai-io/san/internal/tool/agent"
	"github.com/genai-io/san/tests/integration/testutil"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestAgent_ResultSurvivesPersistenceAndRendering(t *testing.T) {
	store, err := session.NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	provider := &testutil.FakeProvider{Responses: []llm.CompletionResponse{
		{Content: ai.TextContent("Verified finding"), StopReason: ai.StopEndTurn, Usage: llm.Usage{Input: 100, Output: 25}},
	}}
	executor := subagent.NewExecutor(provider, t.TempDir(), "worker-model", nil)
	executor.SetSessionStore(store, "parent-1")
	agentTool := agenttool.NewAgentTool()
	agentTool.SetExecutor(subagent.NewExecutorAdapter(executor))
	adapted := tool.AdaptTool(agentTool, core.ToolSchema{Name: "Agent"}, func() string { return "." })
	result, runErr := adapted.Run(context.Background(), ai.ToolCall{ID: "worker-1", Name: "Agent", Input: `{"prompt":"Investigate"}`})
	if runErr != nil {
		t.Fatal(runErr)
	}
	defer tool.PopSideEffect("worker-1")
	text := sdkagent.ResultText(result, runErr)
	if strings.Contains(text, "Tokens:") || !strings.Contains(text, "Transcript:") {
		t.Fatalf("model result must contain findings and inspection pointers: %q", text)
	}
	rec := mustCreateSession(t, store, session.CreateOptions{ID: "parent-1", AgentID: "main", Provider: "p", Model: "m", MaxTokens: 1000})
	owner := core.Message{ID: "call-1", Role: ai.RoleAssistant, Content: ai.Content{{Type: ai.BlockToolCall, ToolCall: &ai.ToolCall{
		ID: "worker-1", Name: "Agent", Input: `{"prompt":"Investigate"}`,
	}}}}
	rec.RecordEvent(sdkagent.MessageAdded{Message: owner})
	rec.RecordEvent(sdkagent.ToolEnd{ID: "worker-1", Name: "Agent", Result: result})
	msg := core.ToolResultMessage(core.ToolResult{ToolCallID: "worker-1", Content: ai.TextContent(text)})
	msg.ID = "result-1"
	rec.RecordEvent(sdkagent.MessageAdded{Message: msg})
	loaded, err := store.Read("parent-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.ChatRows()) != 2 || loaded.ChatRows()[1].ToolDetails == nil {
		t.Fatalf("agent details lost on resume: %+v", loaded.ChatRows())
	}
	data := conv.ToolResultData{ToolName: "Agent", Content: loaded.ChatRows()[1].ToolResult.Content.Text(), Details: loaded.ChatRows()[1].ToolDetails, Width: 100}
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

func mustCreateSession(t *testing.T, store *session.Store, opts session.CreateOptions) *session.Session {
	t.Helper()
	opened, err := store.Create(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	return opened
}
