package subagent

import (
	"context"
	"iter"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/genai-io/san/internal/mcp"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestWorkerSelectsOneMCPTool(t *testing.T) {
	const selected = "mcp__docs__lookup"
	const sibling = "mcp__docs__publish"
	var calls, forbidden atomic.Int32
	server := sdk.NewServer(&sdk.Implementation{Name: "docs", Version: "1"}, nil)
	for name, count := range map[string]*atomic.Int32{"lookup": &calls, "publish": &forbidden} {
		sdk.AddTool(server, &sdk.Tool{Name: name}, func(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, any, error) {
			count.Add(1)
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "fixture result"}}}, nil, nil
		})
	}
	endpoint := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil))
	defer endpoint.Close()
	manager := mcp.NewManagerForTest(map[string]mcp.ServerConfig{"docs": {URL: endpoint.URL}})
	defer manager.Close()
	if err := manager.Connect(t.Context(), "docs"); err != nil {
		t.Fatal(err)
	}
	if len(manager.ToolSchemas()) != 2 {
		t.Fatal("expected both server tools before delegation")
	}
	ask := func(name string) func(context.Context, *ai.Request) iter.Seq2[ai.Delta, error] {
		return func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
			if len(req.Tools) != 1 || req.Tools[0].Name != selected {
				t.Errorf("worker catalogue: %v", req.Tools)
			}
			return aitest.Asks(ai.ToolCall{ID: name, Name: name, Input: `{}`})(ctx, req)
		}
	}
	driver := aitest.New(ask(selected), ask(sibling), aitest.Says("done"))
	e := NewExecutor(continuationProvider{driver}, t.TempDir(), "aitest", nil)
	store, parent := continuationSessions(t, "owner")
	bindContinuationSessions(t, e, store, parent)
	e.SetMCPDependencies(manager)
	e.SetParentTools(func() []string { return []string{selected, sibling} })
	e.SetParentPermissionMode(func() PermissionMode { return PermissionBypass })
	req := tool.AgentExecRequest{Prompt: "use docs", AllowTools: []string{selected, sibling}, DenyTools: []string{sibling}}
	if res, err := e.Run(t.Context(), req); err != nil || !res.Success {
		t.Fatalf("worker: %+v %v", res, err)
	}
	if calls.Load() != 1 || forbidden.Load() != 0 {
		t.Fatalf("MCP server received lookup=%d publish=%d", calls.Load(), forbidden.Load())
	}
	e.SetParentTools(func() []string { return []string{sibling} })
	if _, err := e.pinWorkerPolicy(req); err == nil {
		t.Fatal("worker selected MCP tool absent from parent")
	}
}
