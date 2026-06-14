package selector

import (
	coremcp "github.com/genai-io/san/internal/mcp"
	corepersona "github.com/genai-io/san/internal/persona"
	coreplugin "github.com/genai-io/san/internal/plugin"
	coresetting "github.com/genai-io/san/internal/setting"
	coreskill "github.com/genai-io/san/internal/skill"
)

// State aggregates the modal overlay selectors that render on top of the
// conversation view. It was previously inlined into input.Model; grouping it
// here lets the input package own only true text-input concerns while the
// overlays live in their own package.
type State struct {
	Approval ApprovalModel
	Agent    Agent
	Persona  Persona
	Search   Search
	Skill    SkillState
	Session  SessionState
	Memory   MemoryState
	MCP      MCPState
	Plugin   Plugin
	Provider ProviderState
	Tool     Tool
	Config   Config
}

// Registries carries the registries and settings the overlay selectors need
// at construction time.
type Registries struct {
	AgentRegistry   AgentRegistry
	PersonaRegistry *corepersona.Registry
	SkillRegistry   *coreskill.Registry
	MCPRegistry     *coremcp.Registry
	PluginRegistry  *coreplugin.Registry
	Setting         *coresetting.Settings
	LoadDisabled    func(userLevel bool) map[string]bool
	UpdateDisabled  func(disabled map[string]bool, userLevel bool) error
}

// NewState constructs every overlay selector from the supplied dependencies.
func NewState(deps Registries) State {
	return State{
		Approval: NewApproval(),
		Agent:    NewAgent(deps.AgentRegistry),
		Persona:  NewPersona(deps.PersonaRegistry, deps.Setting),
		Search:   NewSearch(deps.Setting),
		Skill:    SkillState{Selector: NewSkill(deps.SkillRegistry)},
		Session:  SessionState{Selector: NewSession()},
		Memory:   MemoryState{Selector: NewMemory()},
		MCP:      MCPState{Selector: NewMCP(deps.MCPRegistry)},
		Plugin:   NewPlugin(deps.PluginRegistry),
		Provider: ProviderState{Selector: NewProvider()},
		Tool:     NewTool(deps.LoadDisabled, deps.UpdateDisabled),
		Config:   NewConfig(deps.Setting),
	}
}
