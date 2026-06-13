// Source 1 overlay message routing.
//
// Key dispatch and submit handling remain in root app/ because they are
// cross-cutting — touching conv, mode, agent session, and runtime.
// This file routes overlay-specific messages (provider, MCP, plugin, session, etc.)
// through the OverlayDeps struct which provides direct access to concrete state.
package input

import (
	tea "github.com/charmbracelet/bubbletea"
)

// Update routes Source 1 overlay messages to the appropriate handler.
func Update(deps OverlayDeps, msg tea.Msg) (tea.Cmd, bool) {
	if cmd, ok := UpdateProvider(deps, &deps.State.Provider, msg); ok {
		return cmd, true
	}
	if cmd, ok := UpdateMCP(deps, &deps.State.MCP, msg); ok {
		return cmd, true
	}
	if cmd, ok := UpdatePlugin(deps, &deps.State.Plugin, msg); ok {
		return cmd, true
	}
	if cmd, ok := UpdateSession(deps, &deps.State.Session, msg); ok {
		return cmd, true
	}
	if cmd, ok := UpdateMemory(deps, &deps.State.Memory, msg); ok {
		return cmd, true
	}
	if cmd, ok := UpdateSearch(deps, &deps.State.Search, msg); ok {
		return cmd, true
	}
	return nil, false
}
