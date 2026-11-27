package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/mcp"
)

type mcpToolsChangedMsg struct{}

func (m *model) bindMCPTools() {
	if m.mcpToolsChanged == nil {
		m.mcpToolsChanged = make(chan struct{}, 1)
	}
	changes := m.mcpToolsChanged
	m.features.MCP.SetOnToolsChanged(func() {
		select {
		case changes <- struct{}{}:
		default:
		}
	})
	m.syncMCPTools()
}

func awaitMCPToolsChanged(changes <-chan struct{}) tea.Cmd {
	if changes == nil {
		return nil
	}
	return func() tea.Msg {
		<-changes
		return mcpToolsChangedMsg{}
	}
}

// Reconcile current services on the UI loop, even for pre-reload signals.
func (m *model) syncMCPTools() {
	if m.features.Agent == nil {
		return
	}
	tools := m.features.Agent.Tools()
	if tools == nil {
		return
	}
	mcp.ReconcileTools(tools, mcp.AsCoreTools(m.features.MCP.ToolSchemas(), m.features.MCP), m.features.Setting.DisabledTools())
}
