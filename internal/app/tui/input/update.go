// Source 1 overlay message routing.
//
// Key dispatch and submit handling remain in root app/ because they are
// cross-cutting — touching conv, mode, agent session, and runtime.
// This file routes overlay-specific messages (provider, MCP, plugin, session, etc.)
// through the OverlayContext struct which provides direct access to concrete state.
package input

import (
	tea "charm.land/bubbletea/v2"
)

// Update routes Source 1 overlay messages to the appropriate handler.
func Update(overlay OverlayContext, msg tea.Msg) (tea.Cmd, bool) {
	if cmd, ok := overlay.State.Flow.Update(msg); ok {
		return cmd, true
	}
	if cmd, ok := UpdateProvider(overlay, &overlay.State.Provider, msg); ok {
		return cmd, true
	}
	if cmd, ok := UpdateMCP(overlay, &overlay.State.MCP, msg); ok {
		return cmd, true
	}
	if cmd, ok := UpdatePlugin(overlay, &overlay.State.Plugin, msg); ok {
		return cmd, true
	}
	if cmd, ok := UpdateSession(overlay, &overlay.State.Session, msg); ok {
		return cmd, true
	}
	if cmd, ok := UpdateMemory(overlay, &overlay.State.Memory, msg); ok {
		return cmd, true
	}
	if cmd, ok := UpdateSearch(overlay, &overlay.State.Search, msg); ok {
		return cmd, true
	}
	if cmd, ok := UpdatePersona(overlay, &overlay.State.Persona, msg); ok {
		return cmd, true
	}
	return nil, false
}
