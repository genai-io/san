package tool

import (
	"strings"

	"github.com/genai-io/san/internal/core"
)

// parentOnlyTools are tools that only the main conversation can use.
// Subagents never get these regardless of their allow list. Agent is here
// because the agent model is flat: only main spawns workers.
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
// If Static is non-nil, it is returned directly (for fixed agent tool sets).
// Otherwise, tools are resolved dynamically using the config fields.
type Set struct {
	Static         []core.ToolSchema        // fixed tool list (overrides dynamic)
	Disabled       map[string]bool          // excluded tools
	MCP            func() []core.ToolSchema // MCP tools getter
	AgentDirectory func() string            // available-agents listing for the Agent tool description
	Allow          []string                 // agent allow list (nil = all tools, non-nil = only these)
	Disallow       []string                 // agent deny list (excluded after allow filtering)
	IsAgent        bool                     // true for subagent tool sets (excludes parent-only tools)
	ExtraTools     []core.ToolSchema        // caller-built conditional tools (e.g. Evolve; main agent only)
	disallowSet    map[string]bool          // eagerly-initialized normalized lookup cache for Disallow
}

// Tools returns the resolved tool set for a turn.
func (s *Set) Tools() []core.ToolSchema {
	// Static tools override everything
	if s.Static != nil {
		return s.Static
	}

	// Agent with explicit allow list
	if s.Allow != nil {
		return s.agentTools()
	}

	// Agent with nil allow = all tools minus parent-only
	if s.IsAgent {
		return s.agentAllTools()
	}

	// Default mode: full set with disabled/plan filtering
	return s.defaultTools()
}

// defaultTools returns the full tool set filtered by disabled tools.
func (s *Set) defaultTools() []core.ToolSchema {
	tools := GetToolSchemasWith(SchemaOptions{
		MCPTools:       s.MCP,
		AgentDirectory: s.AgentDirectory,
		ExtraTools:     s.ExtraTools,
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

// agentAllTools returns all tools except parent-only and disallowed tools.
// Used for agents with nil Allow (= all tools).
func (s *Set) agentAllTools() []core.ToolSchema {
	allTools := GetToolSchemasWith(SchemaOptions{
		MCPTools:       s.MCP,
		AgentDirectory: s.AgentDirectory,
	})
	filtered := make([]core.ToolSchema, 0, len(allTools))
	for _, t := range allTools {
		if !parentOnlyTools[t.Name] && !s.isDisallowed(t.Name) {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

// agentTools returns tools filtered by the allow list.
// Only tools in the Allow list are included (parent-only tools are excluded
// even when listed). MCP tools matching the allow list (e.g.
// "mcp__server__tool") are also included.
func (s *Set) agentTools() []core.ToolSchema {
	allTools := GetToolSchemas()

	// Build allow set for fast lookup
	allowSet := make(map[string]bool, len(s.Allow))
	for _, name := range s.Allow {
		allowSet[strings.ToLower(name)] = true
	}

	filtered := make([]core.ToolSchema, 0, len(s.Allow))
	for _, t := range allTools {
		if allowSet[strings.ToLower(t.Name)] && !parentOnlyTools[t.Name] && !s.isDisallowed(t.Name) {
			filtered = append(filtered, t)
		}
	}

	// Include MCP tools that match the allow list
	if s.MCP != nil {
		for _, t := range s.MCP() {
			if allowSet[strings.ToLower(t.Name)] && !s.isDisallowed(t.Name) {
				filtered = append(filtered, t)
			}
		}
	}

	return filtered
}

// InitDisallowSet builds the normalized lookup cache for Disallow.
// Must be called before concurrent access to Tools().
func (s *Set) InitDisallowSet() {
	if len(s.Disallow) == 0 {
		return
	}
	s.disallowSet = make(map[string]bool, len(s.Disallow))
	for _, d := range s.Disallow {
		s.disallowSet[strings.ToLower(d)] = true
	}
}

// isDisallowed checks if a tool name is in the Disallow list.
func (s *Set) isDisallowed(name string) bool {
	if len(s.disallowSet) == 0 {
		return false
	}
	return s.disallowSet[strings.ToLower(name)]
}
