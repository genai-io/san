package mcp

import (
	"context"
	"fmt"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

// mcpCoreTool wraps an MCP tool as a core.Tool for use with core.Agent.
type mcpCoreTool struct {
	schema  core.ToolSchema
	servers *Registry
}

func (t *mcpCoreTool) Schema() core.ToolSchema { return t.schema }

func (t *mcpCoreTool) Run(ctx context.Context, call ai.ToolCall) (agent.Result, error) {
	input, _ := core.ParseToolInput(call.Input)
	result, err := t.servers.CallTool(ctx, t.schema.Name, input)
	if err != nil {
		return agent.Result{}, err
	}
	content := ExtractContent(result.Content)
	if result.IsError {
		return agent.TextResult(content), fmt.Errorf("%s", content)
	}
	return agent.TextResult(content), nil
}

// AsCoreTools converts MCP tool schemas into core.Tool implementations
// that route execution through servers.
func AsCoreTools(schemas []core.ToolSchema, servers *Registry) []core.Tool {
	if servers == nil || len(schemas) == 0 {
		return nil
	}
	out := make([]core.Tool, 0, len(schemas))
	for _, schema := range schemas {
		if !IsMCPTool(schema.Name) {
			continue
		}
		out = append(out, &mcpCoreTool{schema: schema, servers: servers})
	}
	return out
}
