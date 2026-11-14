package app

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
	m.services.MCP.SetOnToolsChanged(func() {
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
	if m.services.Agent == nil {
		return
	}
	tools := m.services.Agent.Tools()
	if tools == nil {
		return
	}
	disabled := m.services.Setting.DisabledTools()
	live := make(map[string]bool)
	for _, t := range mcp.AsCoreTools(m.services.MCP.ToolSchemas(), m.services.MCP) {
		name := t.Schema().Name
		if disabled[name] {
			continue
		}
		live[name] = true
		tools.Add(t, "mcp:"+name)
	}
	for _, schema := range tools.Schemas() {
		if mcp.IsMCPTool(schema.Name) && !live[schema.Name] {
			tools.Remove(schema.Name, "mcp:"+schema.Name)
		}
	}
}
