package input

import "github.com/genai-io/san/internal/setting"

// RebindSelectors rebinds pickers without changing input, queues or the Reflection popup.
// Replaced registries close stale pickers; unchanged plugin operations survive.
func (m *Model) RebindSelectors(services SelectorServices) {
	if m.Agent.registry != services.AgentRegistry {
		m.Agent = NewAgentSelector(services.AgentRegistry)
	}
	if m.Persona.registry != services.PersonaRegistry {
		m.Persona = NewPersonaSelector(services.PersonaRegistry, services.Setting)
	} else {
		m.Persona.settingSvc = services.Setting
	}
	m.Search.settingSvc = services.Setting
	if m.Skill.Selector.registry != services.SkillRegistry {
		m.Skill.Selector = NewSkillSelector(services.SkillRegistry)
	}
	if m.MCP.Selector.registry != services.MCPRegistry {
		m.MCP.Selector = NewMCPSelector(services.MCPRegistry)
	}
	if m.Plugin.registry != services.PluginRegistry {
		m.Plugin = NewPluginSelector(services.PluginRegistry)
	}
	m.Tool.loadDisabled = services.LoadDisabled
	m.Tool.saveDisabled = services.UpdateDisabled
	m.Tool.heldOn = services.HeldOn
	// Refresh policy without resetting the open picker's navigation.
	if m.Tool.IsActive() {
		m.Tool.disabledByScope = make(map[setting.Scope]map[string]bool)
		for _, scope := range []setting.Scope{setting.ScopeProject, setting.ScopeUser} {
			if services.LoadDisabled != nil {
				m.Tool.disabledByScope[scope] = services.LoadDisabled(scope)
			}
		}
		m.Tool.held = nil
		if services.HeldOn != nil {
			m.Tool.held = services.HeldOn()
		}
	}
	m.Settings.SetSettings(services.Setting)
}
