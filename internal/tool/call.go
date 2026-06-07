package tool

import (
	"context"
	"fmt"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool/toolresult"
)

// PreparedToolCall is a parsed and validated tool call that can be reused
// across permission checks, interactive handling, and execution.
type PreparedToolCall struct {
	Call   core.ToolCall
	Params map[string]any
	Tool   Tool
	IsMCP  bool
}

// PrepareToolCall parses tool input and resolves the call target against either
// the built-in tool registry or the provided MCP executor.
func PrepareToolCall(tc core.ToolCall, mcpExec MCPExecutor) (*PreparedToolCall, error) {
	params, err := core.ParseToolInput(tc.Input)
	if err != nil {
		return nil, fmt.Errorf("error parsing tool input: %w", err)
	}

	if resolved, ok := Get(tc.Name); ok {
		return &PreparedToolCall{
			Call:   tc,
			Params: params,
			Tool:   resolved,
		}, nil
	}

	if mcpExec != nil && mcpExec.IsMCPTool(tc.Name) {
		return &PreparedToolCall{
			Call:   tc,
			Params: params,
			IsMCP:  true,
		}, nil
	}

	return nil, fmt.Errorf("unknown tool: %s", tc.Name)
}

// Execute runs a prepared tool call against its resolved implementation.
func (p *PreparedToolCall) Execute(ctx context.Context, cwd string, approved bool, mcpExec MCPExecutor) (toolresult.ToolResult, error) {
	if p == nil {
		return toolresult.ToolResult{}, fmt.Errorf("prepared tool call is nil")
	}

	if p.IsMCP {
		if mcpExec == nil {
			return toolresult.ToolResult{}, fmt.Errorf("mcp executor not configured for tool: %s", p.Call.Name)
		}
		return mcpExec.ExecuteMCP(ctx, p.Call.Name, p.Params)
	}

	if p.Tool == nil {
		return toolresult.ToolResult{}, fmt.Errorf("tool not resolved: %s", p.Call.Name)
	}

	if approved {
		if pat, ok := p.Tool.(PermissionAwareTool); ok && pat.RequiresPermission() {
			return pat.ExecuteApproved(ctx, p.Params, cwd), nil
		}
	}

	return p.Tool.Execute(ctx, p.Params, cwd), nil
}
