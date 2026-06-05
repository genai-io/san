package input

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/genai-io/san/v2/internal/app/kit"
)

// AgentConfigInfo holds display info for a single agent configuration.
type AgentConfigInfo struct {
	Name           string
	Description    string
	Color          string
	Model          string
	PermissionMode string
	Tools          []string // nil = all tools
	SourceFile     string
	// Source indicates where the agent definition came from:
	// "built-in", "user", "project", or "plugin". Empty defaults to project.
	Source string
}

// AgentRegistry provides agent display and management for the selector UI.
type AgentRegistry interface {
	ListConfigs() []AgentConfigInfo
	GetDisabledAt(userLevel bool) map[string]bool
	SetEnabled(name string, enabled bool, userLevel bool) error
}

// agentTab identifies a category tab in the agent selector.
type agentTab int

const (
	agentTabBuiltin agentTab = iota
	agentTabProject
	agentTabUser
)

func (t agentTab) String() string {
	switch t {
	case agentTabBuiltin:
		return "Built-in"
	case agentTabProject:
		return "Project"
	case agentTabUser:
		return "User"
	}
	return ""
}

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

// AgentSelector holds the state for the agent selector overlay.
type AgentSelector struct {
	registry       AgentRegistry
	active         bool
	agents         []agentItem // all loaded, before filtering
	filteredAgents []agentItem // after tab + search filter
	nav            kit.ListNav
	width          int
	height         int
	activeTab      agentTab
}

func NewAgentSelector(reg AgentRegistry) AgentSelector {
	return AgentSelector{
		registry:  reg,
		agents:    []agentItem{},
		nav:       kit.ListNav{MaxVisible: 10},
		activeTab: agentTabProject,
	}
}

// EnterSelect activates the selector and loads agents from the registry.
func (s *AgentSelector) EnterSelect(width, height int) error {
	allConfigs := s.registry.ListConfigs()
	disabledByLevel := map[bool]map[string]bool{
		false: s.registry.GetDisabledAt(false),
		true:  s.registry.GetDisabledAt(true),
	}

	s.agents = make([]agentItem, 0, len(allConfigs))
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
		s.agents = append(s.agents, agentItem{
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

	s.active = true
	s.width = width
	s.height = height
	s.nav.Reset()
	s.activeTab = s.firstNonEmptyTab()
	s.updateFilter()
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

func (s *AgentSelector) IsActive() bool { return s.active }

func (s *AgentSelector) Cancel() {
	s.active = false
	s.agents = []agentItem{}
	s.filteredAgents = []agentItem{}
	s.nav.Reset()
	s.nav.Total = 0
}

// agentMatchesTab returns true when an agent belongs to the given tab.
// Plugin agents are folded into Project or User tab depending on install path
// (a "user-plugin" source is treated as User; bare "plugin" defaults to Project).
func agentMatchesTab(a agentItem, tab agentTab) bool {
	switch tab {
	case agentTabBuiltin:
		return a.Source == "built-in"
	case agentTabProject:
		return a.Source == "project" || a.Source == "plugin" || a.Source == "project-plugin"
	case agentTabUser:
		return a.Source == "user" || a.Source == "user-plugin"
	}
	return false
}

func (s *AgentSelector) tabCount(tab agentTab) int {
	count := 0
	for _, a := range s.agents {
		if agentMatchesTab(a, tab) {
			count++
		}
	}
	return count
}

func (s *AgentSelector) firstNonEmptyTab() agentTab {
	for _, t := range []agentTab{agentTabProject, agentTabUser, agentTabBuiltin} {
		if s.tabCount(t) > 0 {
			return t
		}
	}
	return agentTabProject
}

// updateFilter rebuilds filteredAgents from the active tab + search query.
func (s *AgentSelector) updateFilter() {
	query := strings.ToLower(s.nav.Search)
	s.filteredAgents = s.filteredAgents[:0]
	for _, a := range s.agents {
		if !agentMatchesTab(a, s.activeTab) {
			continue
		}
		if query != "" {
			if !kit.FuzzyMatch(strings.ToLower(a.Name), query) &&
				!kit.FuzzyMatch(strings.ToLower(a.Description), query) {
				continue
			}
		}
		s.filteredAgents = append(s.filteredAgents, a)
	}
	s.nav.ResetCursor()
	s.nav.Total = len(s.filteredAgents)
}

func (s *AgentSelector) saveLevelForActiveTab() bool {
	// Built-in and User tabs persist disable-state at the user level;
	// Project tab persists at the project level.
	return s.activeTab != agentTabProject
}

func (s *AgentSelector) Toggle() tea.Cmd {
	if len(s.filteredAgents) == 0 || s.nav.Selected >= len(s.filteredAgents) {
		return nil
	}
	selected := &s.filteredAgents[s.nav.Selected]
	selected.Enabled = !selected.Enabled
	for i := range s.agents {
		if s.agents[i].Name == selected.Name {
			s.agents[i].Enabled = selected.Enabled
			break
		}
	}
	_ = s.registry.SetEnabled(selected.Name, selected.Enabled, s.saveLevelForActiveTab())
	return func() tea.Msg {
		return AgentToggleMsg{AgentName: selected.Name, Enabled: selected.Enabled}
	}
}

func (s *AgentSelector) cycleTab(delta int) {
	tabs := []agentTab{agentTabBuiltin, agentTabProject, agentTabUser}
	idx := 0
	for i, t := range tabs {
		if t == s.activeTab {
			idx = i
			break
		}
	}
	n := len(tabs)
	next := tabs[((idx+delta)%n+n)%n]
	s.activeTab = next
	s.updateFilter()
}

func (s *AgentSelector) HandleKeypress(key tea.KeyMsg) tea.Cmd {
	switch key.Type {
	case tea.KeyTab, tea.KeyRight:
		s.cycleTab(+1)
		return nil
	case tea.KeyShiftTab, tea.KeyLeft:
		s.cycleTab(-1)
		return nil
	case tea.KeyEnter:
		return s.Toggle()
	}
	searchChanged, consumed := s.nav.HandleKey(key)
	if searchChanged {
		s.updateFilter()
	}
	if consumed {
		return nil
	}
	if key.Type == tea.KeyEsc {
		s.Cancel()
		return func() tea.Msg { return kit.DismissedMsg{} }
	}
	return nil
}

// ── Rendering ──────────────────────────────────────────────────────────────────

func (s *AgentSelector) Render() string {
	if !s.active {
		return ""
	}

	panel := kit.Panel{Width: s.width, Height: s.height}

	// Resize the visible window to fit the body height. Each item renders on
	// 2 lines (row + spacer); the selected item adds 1 description sub-line.
	// Reserve 2 lines for more-above/more-below indicators.
	s.nav.MaxVisible = max(3, (panel.BodyHeight()-2)/2)
	s.nav.EnsureVisible()

	var sb strings.Builder

	sb.WriteString(panel.SeparatorLine())
	sb.WriteString("\n")
	sb.WriteString(s.renderTabs())
	sb.WriteString("\n\n")
	sb.WriteString(kit.RenderSearchBox(kit.SearchBoxOpts{
		Query:       s.nav.Search,
		Placeholder: "Type to filter agents...",
		Filtered:    len(s.filteredAgents),
		Total:       s.tabCount(s.activeTab),
		Width:       panel.ContentWidth(),
	}))
	sb.WriteString("\n\n")

	var body strings.Builder
	if len(s.filteredAgents) == 0 {
		body.WriteString(s.renderEmpty())
	} else {
		s.renderItemList(&body, panel)
	}
	sb.WriteString(panel.PadViewport(body.String()))

	sb.WriteString("\n")
	sb.WriteString(panel.SeparatorLine())
	sb.WriteString("\n")
	sb.WriteString(s.renderHints())

	return panel.Wrap(sb.String())
}

func (s *AgentSelector) renderTabs() string {
	tabs := []kit.PanelTab{
		{Name: agentTabBuiltin.String(), Count: s.tabCount(agentTabBuiltin), Show: true, Disable: s.tabCount(agentTabBuiltin) == 0},
		{Name: agentTabProject.String(), Count: s.tabCount(agentTabProject), Show: true},
		{Name: agentTabUser.String(), Count: s.tabCount(agentTabUser), Show: true},
	}
	return kit.RenderPanelTabs(tabs, int(s.activeTab))
}

func (s *AgentSelector) renderEmpty() string {
	if len(s.agents) == 0 {
		return kit.DimStyle().PaddingLeft(2).Render("No agents available")
	}
	if s.tabCount(s.activeTab) == 0 {
		return kit.DimStyle().PaddingLeft(2).Render(
			fmt.Sprintf("No %s agents — press Tab to switch tabs",
				strings.ToLower(s.activeTab.String())))
	}
	return kit.DimStyle().PaddingLeft(2).Render("No agents match the filter")
}

func (s *AgentSelector) renderItemList(sb *strings.Builder, panel kit.Panel) {
	startIdx, endIdx := s.nav.VisibleRange()

	if startIdx > 0 {
		sb.WriteString(kit.MoreAbove())
		sb.WriteString("\n")
	}

	// Compute name column width based on visible items so the model/mode
	// columns align nicely while still adapting to long names.
	maxNameLen := 12
	for i := startIdx; i < endIdx; i++ {
		if l := len(s.filteredAgents[i].Name); l > maxNameLen {
			maxNameLen = l
		}
	}
	maxNameLen = min(maxNameLen, 28)

	descStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	badge := kit.BadgeStyle()

	for i := startIdx; i < endIdx; i++ {
		a := s.filteredAgents[i]

		var statusIcon string
		var statusStyle lipgloss.Style
		if a.Enabled {
			statusIcon = "●"
			statusStyle = kit.SelectorStatusConnected()
		} else {
			statusIcon = "○"
			statusStyle = kit.SelectorStatusNone()
		}

		name := kit.TruncateText(a.Name, maxNameLen)
		paddedName := name + strings.Repeat(" ", max(0, maxNameLen-len(name)))

		model := kit.TruncateText(a.Model, 14)
		paddedModel := model + strings.Repeat(" ", max(0, 14-len(model)))

		mode := kit.TruncateText(a.PermissionMode, 8)
		paddedMode := mode + strings.Repeat(" ", max(0, 8-len(mode)))

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

		// Render the row without SelectorSelectedStyle/SelectorItemStyle's
		// PaddingLeft(2) so the row's left edge lines up with tabs/search/
		// separator. Width(...) right-pads each row to the full inner content
		// area so the right edge also matches the separator line.
		rowWidth := max(20, panel.ContentWidth()-4)
		if i == s.nav.Selected {
			sb.WriteString(lipgloss.NewStyle().
				Foreground(kit.CurrentTheme.TextBright).
				Bold(true).
				Width(rowWidth).
				Render("> " + line))
		} else {
			sb.WriteString(lipgloss.NewStyle().
				Foreground(kit.CurrentTheme.Text).
				Width(rowWidth).
				Render("  " + line))
		}
		sb.WriteString("\n")

		// Description sub-line aligned under the agent name (4 cols in:
		// 2 cursor + 1 icon + 1 space).
		if i == s.nav.Selected && a.Description != "" {
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

	if endIdx < len(s.filteredAgents) {
		sb.WriteString(kit.MoreBelow())
		sb.WriteString("\n")
	}
}

func (s *AgentSelector) renderHints() string {
	return kit.HintLine(
		"↑/↓ navigate",
		"Enter toggle",
		"←/→/Tab switch tab",
		"Esc cancel",
	)
}
