// input panel message routing.
//
// Key dispatch and submit handling remain in root app/ because they are
// cross-cutting — touching conv, mode, agent session, and runtime.
// This file routes panel-specific messages (provider, MCP, plugin, session, etc.)
// through the PanelContext struct which provides direct access to concrete state.
package input

import (
	tea "charm.land/bubbletea/v2"
)

// Update routes input panel messages to the appropriate handler.
func Update(panel PanelContext, msg tea.Msg) (tea.Cmd, bool) {
	if cmd, ok := panel.State.Flow.Update(msg); ok {
		return cmd, true
	}
	if cmd, ok := UpdateProvider(panel, &panel.State.Provider, msg); ok {
		return cmd, true
	}
	if cmd, ok := UpdateMCP(panel, &panel.State.MCP, msg); ok {
		return cmd, true
	}
	if cmd, ok := UpdatePlugin(panel, &panel.State.Plugin, msg); ok {
		return cmd, true
	}
	if cmd, ok := UpdateSession(panel, &panel.State.Session, msg); ok {
		return cmd, true
	}
	if cmd, ok := UpdateMemory(panel, &panel.State.Memory, msg); ok {
		return cmd, true
	}
	if cmd, ok := UpdateSearch(panel, &panel.State.Search, msg); ok {
		return cmd, true
	}
	if cmd, ok := UpdatePersona(panel, &panel.State.Persona, msg); ok {
		return cmd, true
	}
	return nil, false
}
