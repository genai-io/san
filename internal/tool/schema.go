package tool

import "github.com/genai-io/san/v2/internal/core"

// Tool name constants used in runtime comparisons across the codebase.
const (
	ToolAgent       = "Agent"
	ToolSendMessage = "SendMessage"
	ToolTaskOutput  = "TaskOutput"
	ToolTaskStop    = "TaskStop"

	// Deprecated aliases — kept for backward compatibility with cached model contexts.
	ToolAgentOutput   = ToolTaskOutput
	ToolAgentStop     = ToolTaskStop
	ToolSkill         = "Skill"
	ToolTaskCreate    = "TaskCreate"
	ToolTaskGet       = "TaskGet"
	ToolTaskUpdate    = "TaskUpdate"
	ToolTaskList      = "TaskList"
	ToolCronCreate    = "CronCreate"
	ToolCronDelete    = "CronDelete"
	ToolCronList      = "CronList"
	ToolEnterWorktree = "EnterWorktree"
	ToolExitWorktree  = "ExitWorktree"

	ToolAskUserQuestion = "AskUserQuestion"
)

// IsAgentToolName reports whether the tool name represents an agent-like worker tool.
func IsAgentToolName(name string) bool {
	return name == ToolAgent || name == ToolSendMessage
}

// SchemaOptions configures dynamic content embedded in tool schemas.
//
// Both fields are getters (called at schema build time) so tool descriptions
// stay in sync with whatever the harness has loaded most recently. Either may
// be nil — a nil MCPTools yields no MCP tools, and a nil AgentDirectory yields
// an Agent tool whose description omits the available-types listing (useful
// for subagent contexts where recursive spawning is discouraged).
type SchemaOptions struct {
	MCPTools       func() []core.ToolSchema
	AgentDirectory func() string
}

// GetToolSchemas returns core.ToolSchema definitions for all registered tools
// with no dynamic content (no MCP tools, no agent directory). For
// directory-aware schemas use GetToolSchemasWith.
func GetToolSchemas() []core.ToolSchema {
	return GetToolSchemasWith(SchemaOptions{})
}

// GetToolSchemasWith returns tool schemas with dynamic content from opts.
func GetToolSchemasWith(opts SchemaOptions) []core.ToolSchema {
	var directory string
	if opts.AgentDirectory != nil {
		directory = opts.AgentDirectory()
	}

	tools := make([]core.ToolSchema, 0, 20)
	tools = append(tools, baseToolSchemas()...)
	tools = append(tools, skillToolSchema)
	tools = append(tools, agentToolSchema(directory), sendMessageToolSchema)
	tools = append(tools, trackerToolSchemas...)
	tools = append(tools, cronToolSchemas...)
	tools = append(tools, worktreeToolSchemas...)

	if opts.MCPTools != nil {
		tools = append(tools, opts.MCPTools()...)
	}

	return tools
}

func filterSchemas(all []core.ToolSchema, disabled map[string]bool) []core.ToolSchema {
	if len(disabled) == 0 {
		return all
	}
	filtered := make([]core.ToolSchema, 0, len(all))
	for _, t := range all {
		if !disabled[t.Name] {
			filtered = append(filtered, t)
		}
	}
	return filtered
}
