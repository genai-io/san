package tool

import (
	"context"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/internal/tool/toolresult"
)

type testPermissionAwareTool struct{}

func (t *testPermissionAwareTool) Name() string        { return "TestPermissionAwareTool" }
func (t *testPermissionAwareTool) Description() string { return "test tool" }
func (t *testPermissionAwareTool) Icon() string        { return "t" }
func (t *testPermissionAwareTool) Schema() core.ToolSchema {
	return core.ToolSchema{Name: "TestPermissionAwareTool", Description: "test tool"}
}
func (t *testPermissionAwareTool) Execute(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	return toolresult.ToolResult{Success: true, Output: "execute"}
}
func (t *testPermissionAwareTool) RequiresPermission() bool { return true }
func (t *testPermissionAwareTool) PreparePermission(ctx context.Context, params map[string]any, cwd string) (*perm.PermissionRequest, error) {
	return nil, nil
}
func (t *testPermissionAwareTool) ExecuteApproved(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	return toolresult.ToolResult{Success: true, Output: "approved"}
}

type testMCPExecutor struct {
	handled bool
}

func (e *testMCPExecutor) IsMCPTool(name string) bool {
	return name == "mcp__test__tool"
}

func (e *testMCPExecutor) ExecuteMCP(ctx context.Context, name string, params map[string]any) (toolresult.ToolResult, error) {
	e.handled = true
	return toolresult.ToolResult{Success: true, Output: "mcp"}, nil
}

func TestPrepareToolCallParsesAndResolvesBuiltInTool(t *testing.T) {
	Register(&testPermissionAwareTool{})
	t.Cleanup(func() { Unregister("TestPermissionAwareTool") })

	prepared, err := PrepareToolCall(core.ToolCall{
		ID:    "tc5",
		Name:  "TestPermissionAwareTool",
		Input: `{"path":"x"}`,
	}, nil)
	if err != nil {
		t.Fatalf("PrepareToolCall returned error: %v", err)
	}
	if prepared.Tool == nil {
		t.Fatal("expected resolved tool")
	}
	if prepared.Params["path"] != "x" {
		t.Fatalf("unexpected params: %#v", prepared.Params)
	}
}

func TestPrepareToolCallResolvesMCPTool(t *testing.T) {
	mcpExec := &testMCPExecutor{}

	prepared, err := PrepareToolCall(core.ToolCall{
		ID:    "tc6",
		Name:  "mcp__test__tool",
		Input: `{"query":"ok"}`,
	}, mcpExec)
	if err != nil {
		t.Fatalf("PrepareToolCall returned error: %v", err)
	}
	if !prepared.IsMCP {
		t.Fatal("expected MCP tool to be marked")
	}
	if prepared.Params["query"] != "ok" {
		t.Fatalf("unexpected params: %#v", prepared.Params)
	}
}
