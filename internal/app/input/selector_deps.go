package input

import "github.com/genai-io/san/internal/setting"

// SetSelectorDeps rebinds pickers without changing input, queues or Evolve.
// Replaced registries close stale pickers; unchanged plugin operations survive.
func (m *Model) SetSelectorDeps(deps SelectorDeps) {
	if m.Agent.registry != deps.AgentRegistry {
		m.Agent = NewAgentSelector(deps.AgentRegistry)
	}
	if m.Persona.registry != deps.PersonaRegistry {
		m.Persona = NewPersonaSelector(deps.PersonaRegistry, deps.Setting)
	} else {
		m.Persona.settingSvc = deps.Setting
	}
	m.Search.settingSvc = deps.Setting
	if m.Skill.Selector.registry != deps.SkillRegistry {
		m.Skill.Selector = NewSkillSelector(deps.SkillRegistry)
	}
	if m.MCP.Selector.registry != deps.MCPRegistry {
		m.MCP.Selector = NewMCPSelector(deps.MCPRegistry)
	}
	if m.Plugin.registry != deps.PluginRegistry {
		m.Plugin = NewPluginSelector(deps.PluginRegistry)
	}
	m.Tool.loadDisabled = deps.LoadDisabled
	m.Tool.saveDisabled = deps.UpdateDisabled
	m.Tool.heldOn = deps.HeldOn
	// Refresh policy without resetting the open picker's navigation.
	if m.Tool.IsActive() {
		m.Tool.disabledByScope = make(map[setting.Scope]map[string]bool)
		for _, scope := range []setting.Scope{setting.ScopeProject, setting.ScopeUser} {
			if deps.LoadDisabled != nil {
				m.Tool.disabledByScope[scope] = deps.LoadDisabled(scope)
			}
		}
		m.Tool.held = nil
		if deps.HeldOn != nil {
			m.Tool.held = deps.HeldOn()
		}
	}
	m.Settings.SetSettings(deps.Setting)
}
