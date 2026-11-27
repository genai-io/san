package input

import "github.com/genai-io/san/internal/setting"

// RebindSelectors rebinds pickers without changing input, queues or the Reflection popup.
// Replaced registries close stale pickers; unchanged plugin operations survive.
func (m *Model) RebindSelectors(bindings SelectorBindings) {
	if m.Agent.registry != bindings.AgentRegistry {
		m.Agent = NewAgentSelector(bindings.AgentRegistry)
	}
	if m.Persona.registry != bindings.PersonaRegistry {
		m.Persona = NewPersonaSelector(bindings.PersonaRegistry, bindings.Setting)
	} else {
		m.Persona.settingSvc = bindings.Setting
	}
	m.Search.settingSvc = bindings.Setting
	if m.Skill.Selector.registry != bindings.SkillRegistry {
		m.Skill.Selector = NewSkillSelector(bindings.SkillRegistry)
	}
	if m.MCP.Selector.serverSet != bindings.MCPServers {
		m.MCP.Selector = NewMCPSelector(bindings.MCPServers)
	}
	if m.Plugin.registry != bindings.PluginRegistry {
		m.Plugin = NewPluginSelector(bindings.PluginRegistry)
	}
	m.Tool.loadDisabled = bindings.LoadDisabled
	m.Tool.saveDisabled = bindings.UpdateDisabled
	m.Tool.heldOn = bindings.HeldOn
	// Refresh policy without resetting the open picker's navigation.
	if m.Tool.IsActive() {
		m.Tool.disabledByScope = make(map[setting.Scope]map[string]bool)
		for _, scope := range []setting.Scope{setting.ScopeProject, setting.ScopeUser} {
			if bindings.LoadDisabled != nil {
				m.Tool.disabledByScope[scope] = bindings.LoadDisabled(scope)
			}
		}
		m.Tool.held = nil
		if bindings.HeldOn != nil {
			m.Tool.held = bindings.HeldOn()
		}
	}
	m.Settings.SetSettings(bindings.Setting)
}
