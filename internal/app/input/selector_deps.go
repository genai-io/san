package input

import "github.com/genai-io/san/internal/setting"

// SetSelectorDeps binds selectors to the current project services. Registry
// replacement closes the affected picker and discards its cached rows; reopening
// queries the new registry. Unchanged registries retain picker/operation state.
// Composer, history, queue and pending invocations are not touched.
// Evolve is intentionally constructor-bound: its workspace accessor is live and
// its stores have an application lifetime, so reload must not replace its panels.
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
	// An open tool picker must not write a snapshot from the previous settings
	// service through the new save callback. Keep navigation, refresh policy only.
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
