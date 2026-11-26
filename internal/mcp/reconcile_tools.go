package mcp

import (
	"github.com/genai-io/san/internal/core"
)

// ReconcileTools syncs a tool set with the live MCP catalog, minus disabled tools.
func ReconcileTools(tools *core.Tools, catalog []core.Tool, disabled map[string]bool) {
	live := make(map[string]bool)
	for _, t := range catalog {
		name := t.Schema().Name
		if disabled[name] {
			continue
		}
		live[name] = true
		tools.Add(t, "mcp:"+name)
	}
	for _, schema := range tools.Schemas() {
		if IsMCPTool(schema.Name) && !live[schema.Name] {
			tools.Remove(schema.Name, "mcp:"+schema.Name)
		}
	}
}
