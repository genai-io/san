package mcp

import (
	"context"
	"fmt"

	"github.com/genai-io/san/internal/core"
)

// mcpCoreTool wraps an MCP tool as a core.Tool for use with core.Agent.
type mcpCoreTool struct {
	schema core.ToolSchema
	caller *Caller
}

func (t *mcpCoreTool) Name() string            { return t.schema.Name }
func (t *mcpCoreTool) Description() string     { return t.schema.Description }
func (t *mcpCoreTool) Schema() core.ToolSchema { return t.schema }

func (t *mcpCoreTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	content, isError, err := t.caller.CallTool(ctx, t.schema.Name, input)
	if err != nil {
		return "", err
	}
	if isError {
		return content, fmt.Errorf("%s", content)
	}
	return content, nil
}

// AsCoreTools converts MCP tool schemas into core.Tool implementations
// that route execution through the provided Caller.
func AsCoreTools(schemas []core.ToolSchema, caller *Caller) []core.Tool {
	if caller == nil || len(schemas) == 0 {
		return nil
	}
	tools := make([]core.Tool, 0, len(schemas))
	for _, schema := range schemas {
		if !IsMCPTool(schema.Name) {
			continue
		}
		tools = append(tools, &mcpCoreTool{schema: schema, caller: caller})
	}
	return tools
}
