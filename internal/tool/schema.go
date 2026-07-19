package tool

import "github.com/genai-io/san/internal/core"

// Tool name constants used in runtime comparisons across the codebase.
const (
	ToolRead      = "Read"
	ToolGlob      = "Glob"
	ToolGrep      = "Grep"
	ToolWebFetch  = "WebFetch"
	ToolWebSearch = "WebSearch"
	ToolEdit      = "Edit"
	ToolWrite     = "Write"

	ToolBash        = "Bash"
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

	ToolEvolve = "Evolve"
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

	// ExtraTools are caller-supplied schemas appended to the registered set —
	// the hook for conditionally-present tools whose schema the caller builds
	// (e.g. the self-learning Evolve trigger, built by tool/evolve.Schema and
	// injected only by the main agent). They pass through the same
	// disabled/allow filtering as every other tool.
	ExtraTools []core.ToolSchema
}

// builtinToolOrder is the canonical order the built-in tools are presented to
// the model. Each entry is resolved against the registry and described by the
// tool's own Schema, so this list controls ordering only — a tool's wire
// contract lives with its implementation. Names absent from the registry
// (e.g. the intentionally-disabled TaskOutput) are skipped.
var builtinToolOrder = []string{
	ToolRead, ToolGlob, ToolGrep, ToolWebFetch, ToolWebSearch, ToolEdit, ToolWrite, ToolBash, ToolTaskStop, ToolAskUserQuestion,
	ToolSkill,
	ToolAgent, ToolSendMessage,
	ToolTaskCreate, ToolTaskGet, ToolTaskUpdate, ToolTaskList,
	ToolCronCreate, ToolCronDelete, ToolCronList,
	ToolEnterWorktree, ToolExitWorktree,
}

// GetToolSchemas returns core.ToolSchema definitions for all registered tools
// with no dynamic content (no MCP tools, no agent directory). For
// directory-aware schemas use GetToolSchemasWith.
func GetToolSchemas() []core.ToolSchema {
	return GetToolSchemasWith(SchemaOptions{})
}

// GetToolSchemasWith returns tool schemas with dynamic content from opts.
// Built-in schemas are sourced from each registered tool's Schema (the single
// source of truth), in builtinToolOrder; the Agent tool's description embeds
// the available-agents directory when one is supplied.
func GetToolSchemasWith(opts SchemaOptions) []core.ToolSchema {
	var directory string
	if opts.AgentDirectory != nil {
		directory = opts.AgentDirectory()
	}

	tools := make([]core.ToolSchema, 0, len(builtinToolOrder)+len(opts.ExtraTools)+8)
	for _, name := range builtinToolOrder {
		t, ok := Get(name)
		if !ok {
			continue
		}
		if directory != "" {
			if da, ok := t.(DirectoryAwareTool); ok {
				tools = append(tools, da.SchemaWithDirectory(directory))
				continue
			}
		}
		tools = append(tools, t.Schema())
	}

	tools = append(tools, opts.ExtraTools...)

	if opts.MCPTools != nil {
		tools = append(tools, opts.MCPTools()...)
	}

	return tools
}
