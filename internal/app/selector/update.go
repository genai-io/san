// Source 1 overlay message routing.
//
// Key dispatch and submit handling remain in root app/ because they are
// cross-cutting — touching conv, mode, agent session, and runtime.
// This file routes overlay-specific messages (provider, MCP, plugin, session, etc.)
// through the Runtime struct which provides direct access to concrete state.
package selector

import (
	tea "charm.land/bubbletea/v2"
)

// Update routes Source 1 overlay messages to the appropriate handler.
func Update(rt Runtime, msg tea.Msg) (tea.Cmd, bool) {
	if cmd, ok := UpdateProvider(rt, &rt.State.Provider, msg); ok {
		return cmd, true
	}
	if cmd, ok := UpdateMCP(rt, &rt.State.MCP, msg); ok {
		return cmd, true
	}
	if cmd, ok := UpdatePlugin(rt, &rt.State.Plugin, msg); ok {
		return cmd, true
	}
	if cmd, ok := UpdateSession(rt, &rt.State.Session, msg); ok {
		return cmd, true
	}
	if cmd, ok := UpdateMemory(rt, &rt.State.Memory, msg); ok {
		return cmd, true
	}
	if cmd, ok := UpdateSearch(rt, &rt.State.Search, msg); ok {
		return cmd, true
	}
	if cmd, ok := UpdatePersona(rt, &rt.State.Persona, msg); ok {
		return cmd, true
	}
	return nil, false
}
