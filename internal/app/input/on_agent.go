package input

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/tool"
)

// AgentRegistry provides agent display and management for the selector UI.
type AgentRegistry interface {
	ListConfigs() []tool.AgentConfigInfo
	GetDisabledAt(userLevel bool) map[string]bool
	SetEnabled(name string, enabled bool, userLevel bool) error
}

// agentTab identifies a category tab in the agent selector. The values double
// as indices into the selector's tabbedList tab order.
type agentTab int

const (
	agentTabBuiltin agentTab = iota
	agentTabProject
	agentTabUser
)

type agentItem struct {
	Name           string
	Description    string
	Model          string
	PermissionMode string
	Tools          string
	Source         string // "built-in", "user", "project", "plugin"
	PluginName     string // populated when Source == "plugin" or name has "ns:" prefix
	Enabled        bool
}

// AgentToggleMsg is sent when an agent's enabled state is toggled.
type AgentToggleMsg struct {
	AgentName string
	Enabled   bool
}

// AgentSelector holds the state for the agent selector overlay. The tab/filter/
// keypress/frame mechanics live in the embedded tabbedList; this type owns
// agent loading, the row layout, and the enable/disable action.
type AgentSelector struct {
	registry AgentRegistry
	list     tabbedList[agentItem]
}

func NewAgentSelector(reg AgentRegistry) AgentSelector {
	return AgentSelector{
		registry: reg,
		list: tabbedList[agentItem]{
			tabs: []tabSpec{
				{name: "Built-in", disableIfEmpty: true},
				{name: "Project"},
				{name: "User"},
			},
			// Project first, then User, then Built-in (built-in agents are
			// rarely the thing the user came to toggle).
			preferred:   []int{int(agentTabProject), int(agentTabUser), int(agentTabBuiltin)},
			noun:        "agents",
			placeholder: "Type to filter agents...",
			hints:       []string{"↑/↓ navigate", "Enter toggle", "←/→/Tab switch tab", "Esc cancel"},
			matchesTab:  agentMatchesTab,
			searchKeys:  func(a agentItem) []string { return []string{a.Name, a.Description} },
			nav:         kit.ListNav{MaxVisible: 10},
		},
	}
}

// EnterSelect activates the selector and loads agents from the registry.
func (s *AgentSelector) EnterSelect(width, height int) error {
	allConfigs := s.registry.ListConfigs()
	disabledByLevel := map[bool]map[string]bool{
		false: s.registry.GetDisabledAt(false),
		true:  s.registry.GetDisabledAt(true),
	}

	agents := make([]agentItem, 0, len(allConfigs))
	for _, cfg := range allConfigs {
		lowerName := strings.ToLower(cfg.Name)
		var pluginName string
		if idx := strings.Index(cfg.Name, ":"); idx > 0 {
			pluginName = cfg.Name[:idx]
		}
		source := cfg.Source
		// Disabled state lookup uses the user-level map for built-in/user
		// agents, project-level map for project agents.
		userLevel := source == "user" || source == "built-in"
		agents = append(agents, agentItem{
			Name:           cfg.Name,
			Description:    cfg.Description,
			Model:          cfg.Model,
			PermissionMode: formatAgentPermMode(cfg.PermissionMode),
			Tools:          formatAgentTools(cfg.Tools),
			Source:         source,
			PluginName:     pluginName,
			Enabled:        !disabledByLevel[userLevel][lowerName],
		})
	}

	s.list.load(agents, width, height)
	return nil
}

func formatAgentPermMode(mode string) string {
	switch mode {
	case "explore":
		return "explore"
	case "edit":
		return "edit"
	case "default", "":
		return "default"
	default:
		return mode
	}
}

func formatAgentTools(tools []string) string {
	if tools == nil {
		return "all tools"
	}
	if len(tools) == 0 {
		return "none"
	}
	return strings.Join(tools, ", ")
}

func (s *AgentSelector) IsActive() bool { return s.list.active }

// agentMatchesTab returns true when an agent belongs to the given tab.
// Plugin agents are folded into Project or User tab depending on install path
// (a "user-plugin" source is treated as User; bare "plugin" defaults to Project).
func agentMatchesTab(a agentItem, tab int) bool {
	switch agentTab(tab) {
	case agentTabBuiltin:
		return a.Source == "built-in"
	case agentTabProject:
		return a.Source == "project" || a.Source == "plugin" || a.Source == "project-plugin"
	case agentTabUser:
		return a.Source == "user" || a.Source == "user-plugin"
	}
	return false
}

func (s *AgentSelector) saveLevelForActiveTab() bool {
	// Built-in and User tabs persist disable-state at the user level;
	// Project tab persists at the project level.
	return s.list.activeTab != int(agentTabProject)
}

func (s *AgentSelector) Toggle() tea.Cmd {
	if len(s.list.filtered) == 0 || s.list.nav.Selected >= len(s.list.filtered) {
		return nil
	}
	selected := &s.list.filtered[s.list.nav.Selected]
	selected.Enabled = !selected.Enabled
	for i := range s.list.items {
		if s.list.items[i].Name == selected.Name {
			s.list.items[i].Enabled = selected.Enabled
			break
		}
	}
	_ = s.registry.SetEnabled(selected.Name, selected.Enabled, s.saveLevelForActiveTab())
	return func() tea.Msg {
		return AgentToggleMsg{AgentName: selected.Name, Enabled: selected.Enabled}
	}
}

func (s *AgentSelector) HandleKeypress(key tea.KeyMsg) tea.Cmd {
	return s.list.handleKey(key, s.Toggle)
}

// ── Rendering ──────────────────────────────────────────────────────────────────

func (s *AgentSelector) Render() string {
	return s.list.render(s.renderItemList)
}

func (s *AgentSelector) renderItemList(sb *strings.Builder, panel kit.Panel) {
	startIdx, endIdx := s.list.nav.VisibleRange()

	if startIdx > 0 {
		sb.WriteString(kit.MoreAbove())
		sb.WriteString("\n")
	}

	// Compute name column width based on visible items so the model/mode
	// columns align nicely while still adapting to long names.
	maxNameLen := 12
	for i := startIdx; i < endIdx; i++ {
		if w := lipgloss.Width(s.list.filtered[i].Name); w > maxNameLen {
			maxNameLen = w
		}
	}
	maxNameLen = min(maxNameLen, 28)

	descStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	badge := kit.BadgeStyle()

	for i := startIdx; i < endIdx; i++ {
		a := s.list.filtered[i]

		var statusIcon string
		var statusStyle lipgloss.Style
		if a.Enabled {
			statusIcon = "●"
			statusStyle = kit.SelectorStatusConnected()
		} else {
			statusIcon = "○"
			statusStyle = kit.SelectorStatusNone()
		}

		// Pad by display width, not bytes: one CJK agent name would otherwise
		// shift the model, mode and badge columns on that row only.
		name := kit.TruncateText(a.Name, maxNameLen)
		paddedName := name + strings.Repeat(" ", max(0, maxNameLen-lipgloss.Width(name)))

		model := kit.TruncateText(a.Model, 14)
		paddedModel := model + strings.Repeat(" ", max(0, 14-lipgloss.Width(model)))

		mode := kit.TruncateText(a.PermissionMode, 8)
		paddedMode := mode + strings.Repeat(" ", max(0, 8-lipgloss.Width(mode)))

		// Reserve room for an inline source badge on the right.
		badgeText := ""
		switch {
		case a.PluginName != "":
			badgeText = "[Plugin: " + a.PluginName + "]"
		case a.Source == "built-in":
			badgeText = "[Built-in]"
		}

		// Width budget for one row, accounting for the panel's Padding(1, 2)
		// (4 cols total) plus the row's own decoration:
		//   2 ("> ") + 1 (icon) + 1 (space) + name + 2 (sep) +
		//   14 (model) + 2 (sep) + 8 (mode) + 2 (sep) + tools
		//   [+ 1 space + badge]
		// The trailing -4 is a right-margin safety buffer.
		rowFixed := 2 + 1 + 1 + maxNameLen + 2 + 14 + 2 + 8 + 2
		if badgeText != "" {
			rowFixed += 1 + len(badgeText)
		}
		toolsWidth := max(8, panel.ContentWidth()-4-rowFixed-4)
		tools := kit.TruncateText(a.Tools, toolsWidth)

		line := fmt.Sprintf("%s %s  %s  %s  %s",
			statusStyle.Render(statusIcon),
			paddedName,
			paddedModel,
			paddedMode,
			descStyle.Render(tools),
		)
		if badgeText != "" {
			line += " " + badge.Render(badgeText)
		}

		// Render the row without the selector row styles' PaddingLeft(2) so
		// the row's left edge lines up with tabs/search/
		// separator. Width(...) right-pads each row to the full inner content
		// area so the right edge also matches the separator line.
		rowWidth := max(20, panel.ContentWidth()-4)
		sb.WriteString(kit.RenderPanelRow(line, i == s.list.nav.Selected, rowWidth))
		sb.WriteString("\n")

		// Description sub-line aligned under the agent name (4 cols in:
		// 2 cursor + 1 icon + 1 space).
		if i == s.list.nav.Selected && a.Description != "" {
			subStyle := lipgloss.NewStyle().
				Foreground(kit.CurrentTheme.Muted).
				PaddingLeft(4)
			descLineWidth := max(10, panel.ContentWidth()-8)
			sb.WriteString(subStyle.Render(kit.TruncateText(a.Description, descLineWidth)))
			sb.WriteString("\n")
		}

		// Spacer for breathing room between rows (skip after the last item;
		// PadViewport will handle the trailing blank lines).
		if i < endIdx-1 {
			sb.WriteString("\n")
		}
	}

	if endIdx < len(s.list.filtered) {
		sb.WriteString(kit.MoreBelow())
		sb.WriteString("\n")
	}
}
