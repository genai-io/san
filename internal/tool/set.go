package tool

import "github.com/genai-io/san/internal/core"

// parentOnlyTools are tools that only the main conversation can use.
// Subagents never get these. Agent is here because the agent model is flat:
// only main spawns workers.
//
// The task tracker tools are parent-only for the same reason: the tracker is
// the main conversation's plan, and every conversation shares the one
// process-global todo store. A subagent calling TaskCreate/TaskUpdate would
// leak its private planning into the main panel (showing up as extra rows next
// to the worker item that already represents it), and TaskGet would hand it
// back the main plan it has no business reading.
// Cron is parent-only for the same reason again: scheduling creates state
// that outlives the worker and belongs to the session owner.
var parentOnlyTools = map[string]bool{
	ToolAgent:      true,
	ToolAgentStop:  true,
	ToolTaskCreate: true,
	ToolTaskUpdate: true,
	ToolTaskGet:    true,
	ToolCron:       true,
}

// IsParentOnlyTool reports whether the tool is reserved for the main
// conversation. The subagent permission gate consults this so a
// hallucinated call cannot slip through the safe-tool auto-permit.
func IsParentOnlyTool(name string) bool {
	return parentOnlyTools[name]
}

// Set provides tools for a conversation turn.
// If Static is non-nil, it is returned directly for fixed caller-owned schemas.
// Otherwise, tools are resolved dynamically using the config fields.
type Set struct {
	Static     []core.ToolSchema        // fixed tool list (overrides dynamic)
	Disabled   map[string]bool          // excluded tools
	MCP        func() []core.ToolSchema // MCP tools getter
	IsAgent    bool                     // true for subagent tool sets (excludes parent-only tools)
	ExtraTools []core.ToolSchema        // caller-built conditional tools (e.g. Evolve; main agent only)
}

// Tools returns the resolved tool set for a turn.
func (s *Set) Tools() []core.ToolSchema {
	// Static tools override everything
	if s.Static != nil {
		return s.Static
	}

	// Subagents get all worker tools minus the parent-only set.
	if s.IsAgent {
		return s.agentAllTools()
	}

	// Default mode: full set with disabled/plan filtering
	return s.defaultTools()
}

// defaultTools returns the full tool set filtered by disabled tools.
func (s *Set) defaultTools() []core.ToolSchema {
	tools := GetToolSchemasWith(SchemaOptions{
		MCPTools:   s.MCP,
		ExtraTools: s.ExtraTools,
	})

	filtered := make([]core.ToolSchema, 0, len(tools))
	for _, t := range tools {
		if s.Disabled[t.Name] {
			continue
		}
		filtered = append(filtered, t)
	}
	return filtered
}

// agentAllTools returns all tools except parent-only tools.
func (s *Set) agentAllTools() []core.ToolSchema {
	allTools := GetToolSchemasWith(SchemaOptions{
		MCPTools: s.MCP,
	})
	filtered := make([]core.ToolSchema, 0, len(allTools))
	for _, t := range allTools {
		if !parentOnlyTools[t.Name] {
			filtered = append(filtered, t)
		}
	}
	return filtered
}
