package input

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/core"
	coretool "github.com/genai-io/san/internal/tool"
)

type toolItem struct {
	Name        string
	Description string
	Enabled     bool
}

// ToolSelector holds state for the tool selector overlay.
type ToolSelector struct {
	active        bool
	tools         []toolItem
	filteredTools []toolItem
	nav           kit.ListNav
	width         int
	height        int
	disabledTools map[string]bool
	saveLevel     kit.SaveLevel
	loadDisabled  func(userLevel bool) map[string]bool
	saveDisabled  func(disabled map[string]bool, userLevel bool) error
}

// ToolToggleMsg is sent when a tool's enabled state is toggled.
type ToolToggleMsg struct {
	ToolName string
	Enabled  bool
}

// NewToolSelector creates a new ToolSelector with injected load/save callbacks.
func NewToolSelector(
	loadDisabled func(userLevel bool) map[string]bool,
	saveDisabled func(disabled map[string]bool, userLevel bool) error,
) ToolSelector {
	return ToolSelector{
		active:       false,
		tools:        []toolItem{},
		nav:          kit.ListNav{MaxVisible: 10},
		loadDisabled: loadDisabled,
		saveDisabled: saveDisabled,
	}
}

// EnterSelect enters tool selection mode.
func (s *ToolSelector) EnterSelect(width, height int, disabledTools map[string]bool, mcpTools func() []core.ToolSchema) error {
	allTools := coretool.GetToolSchemasWith(coretool.SchemaOptions{MCPTools: mcpTools})

	s.tools = make([]toolItem, 0, len(allTools))
	for _, t := range allTools {
		s.tools = append(s.tools, toolItem{
			Name:        t.Name,
			Description: t.Description,
			Enabled:     !disabledTools[t.Name],
		})
	}

	s.active = true
	s.width = width
	s.height = height
	s.disabledTools = disabledTools
	s.filteredTools = s.tools
	s.nav.Reset()
	s.nav.Total = len(s.filteredTools)

	return nil
}

// IsActive returns whether the selector is active.
func (s *ToolSelector) IsActive() bool {
	return s.active
}

// Cancel cancels the selector.
func (s *ToolSelector) Cancel() {
	s.active = false
	s.tools = []toolItem{}
	s.filteredTools = []toolItem{}
	s.nav.Reset()
	s.nav.Total = 0
}

func (s *ToolSelector) updateFilter() {
	if s.nav.Search == "" {
		s.filteredTools = s.tools
	} else {
		query := strings.ToLower(s.nav.Search)
		s.filteredTools = make([]toolItem, 0)
		for _, t := range s.tools {
			if kit.FuzzyMatch(strings.ToLower(t.Name), query) ||
				kit.FuzzyMatch(strings.ToLower(t.Description), query) {
				s.filteredTools = append(s.filteredTools, t)
			}
		}
	}
	s.nav.ResetCursor()
	s.nav.Total = len(s.filteredTools)
}

func (s *ToolSelector) reloadToolStates() {
	levelDisabled := s.loadDisabled(s.saveLevel == kit.SaveLevelUser)

	for k := range s.disabledTools {
		delete(s.disabledTools, k)
	}
	for k, v := range levelDisabled {
		s.disabledTools[k] = v
	}

	for i := range s.tools {
		s.tools[i].Enabled = !s.disabledTools[s.tools[i].Name]
	}
	for i := range s.filteredTools {
		s.filteredTools[i].Enabled = !s.disabledTools[s.filteredTools[i].Name]
	}
}

// Toggle toggles the enabled state of the currently selected tool.
func (s *ToolSelector) Toggle() tea.Cmd {
	if len(s.filteredTools) == 0 || s.nav.Selected >= len(s.filteredTools) {
		return nil
	}

	selected := &s.filteredTools[s.nav.Selected]
	selected.Enabled = !selected.Enabled

	for i := range s.tools {
		if s.tools[i].Name == selected.Name {
			s.tools[i].Enabled = selected.Enabled
			break
		}
	}

	if selected.Enabled {
		delete(s.disabledTools, selected.Name)
	} else {
		s.disabledTools[selected.Name] = true
	}

	_ = s.saveDisabled(s.disabledTools, s.saveLevel == kit.SaveLevelUser)

	return func() tea.Msg {
		return ToolToggleMsg{
			ToolName: selected.Name,
			Enabled:  selected.Enabled,
		}
	}
}

// HandleKeypress handles a keypress and returns a command if needed.
func (s *ToolSelector) HandleKeypress(key tea.KeyMsg) tea.Cmd {
	if key.String() == "tab" {
		if s.saveLevel == kit.SaveLevelProject {
			s.saveLevel = kit.SaveLevelUser
		} else {
			s.saveLevel = kit.SaveLevelProject
		}
		s.reloadToolStates()
		return nil
	}

	if key.String() == "enter" {
		return s.Toggle()
	}

	searchChanged, consumed := s.nav.HandleKey(key)
	if searchChanged {
		s.updateFilter()
	}
	if consumed {
		return nil
	}

	if key.String() == "esc" {
		s.Cancel()
		return func() tea.Msg { return kit.DismissedMsg{} }
	}

	return nil
}

// Render renders the tool selector overlay.
func (s *ToolSelector) Render() string {
	if !s.active {
		return ""
	}

	var sb strings.Builder

	levelIndicator := fmt.Sprintf("[%s]", s.saveLevel.String())
	title := fmt.Sprintf("Manage Tools (%d/%d)  %s", len(s.filteredTools), len(s.tools), levelIndicator)
	sb.WriteString(kit.SelectorTitleStyle().Render(title))
	sb.WriteString("\n")

	searchPrompt := "🔍 "
	if s.nav.Search == "" {
		sb.WriteString(kit.SelectorHintStyle().Render(searchPrompt + "Type to filter..."))
	} else {
		sb.WriteString(kit.SelectorBreadcrumbStyle().Render(searchPrompt + s.nav.Search + "▏"))
	}
	sb.WriteString("\n\n")

	boxWidth := kit.CalculateToolBoxWidth(s.width)
	maxDescLen := max(boxWidth-30, 20)

	if len(s.filteredTools) == 0 {
		sb.WriteString(kit.SelectorHintStyle().Render("  No tools match the filter"))
		sb.WriteString("\n")
	} else {
		startIdx, endIdx := s.nav.VisibleRange()

		if startIdx > 0 {
			sb.WriteString(kit.SelectorHintStyle().Render("  ↑ more above"))
			sb.WriteString("\n")
		}

		for i := startIdx; i < endIdx; i++ {
			t := s.filteredTools[i]

			var statusIcon string
			var statusStyle lipgloss.Style
			if t.Enabled {
				statusIcon = "●"
				statusStyle = kit.SelectorStatusConnected()
			} else {
				statusIcon = "○"
				statusStyle = kit.SelectorStatusNone()
			}

			desc := t.Description
			if idx := strings.Index(desc, "\n"); idx != -1 {
				desc = desc[:idx]
			}
			if len(desc) > maxDescLen {
				desc = desc[:maxDescLen-3] + "..."
			}

			descStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
			line := fmt.Sprintf("%s %-15s  %s",
				statusStyle.Render(statusIcon),
				t.Name,
				descStyle.Render(desc),
			)

			if i == s.nav.Selected {
				sb.WriteString(kit.SelectorSelectedStyle().Render("> " + line))
			} else {
				sb.WriteString(kit.SelectorItemStyle().Render("  " + line))
			}
			sb.WriteString("\n")
		}

		if endIdx < len(s.filteredTools) {
			sb.WriteString(kit.SelectorHintStyle().Render("  ↓ more below"))
			sb.WriteString("\n")
		}
	}

	sb.WriteString("\n")
	sb.WriteString(kit.SelectorHintStyle().Render("↑/↓ navigate · Enter toggle · Tab level · Esc cancel"))

	content := sb.String()
	box := kit.SelectorBorderStyle().Width(boxWidth).Render(content)

	return lipgloss.Place(s.width, s.height-4, lipgloss.Center, lipgloss.Center, box)
}
