package input

import (
	"fmt"
	"maps"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/setting"
	coretool "github.com/genai-io/san/internal/tool"
)

type toolItem struct {
	Name        string
	Description string
	Enabled     bool
}

// toolTab identifies a save-level tab in the tool selector. Unlike the agent
// and skill tabs — which partition items by source — every tool appears under
// both tabs; the tab only picks which settings level (project vs user) drives
// the shown state and receives the toggle. The values double as indices into
// the selector's tabbedList tab order.
type toolTab int

const (
	toolTabProject toolTab = iota
	toolTabUser
)

// ToolToggleMsg is sent when a tool's enabled state is toggled.
type ToolToggleMsg struct {
	ToolName string
	Enabled  bool
}

// ToolSelector holds the state for the tool selector overlay. The tab/filter/
// keypress/frame mechanics live in the embedded tabbedList; this type owns tool
// loading, the row layout, and the enable/disable action. It matches the agent
// and skill overlays so the three read as one family.
type ToolSelector struct {
	list         tabbedList[toolItem]
	loadDisabled func(userLevel bool) map[string]bool
	saveDisabled func(disabled map[string]bool, userLevel bool) error
	// disabledByLevel holds a working copy of each level's explicit disabled
	// entries (keyed by userLevel), loaded once per open and mutated by Toggle.
	disabledByLevel map[bool]map[string]bool
}

// NewToolSelector creates a new ToolSelector with injected load/save callbacks.
func NewToolSelector(
	loadDisabled func(userLevel bool) map[string]bool,
	saveDisabled func(disabled map[string]bool, userLevel bool) error,
) ToolSelector {
	return ToolSelector{
		loadDisabled: loadDisabled,
		saveDisabled: saveDisabled,
		list: tabbedList[toolItem]{
			tabs: []tabSpec{
				{name: "Project"},
				{name: "User"},
			},
			noun:        "tools",
			placeholder: "Type to filter tools...",
			hints:       []string{"↑/↓ navigate", "Enter toggle", "←/→/Tab switch level", "Esc close"},
			// Every tool is manageable at both levels, so no tab filters it out.
			matchesTab: func(toolItem, int) bool { return true },
			searchKeys: func(t toolItem) []string { return []string{t.Name, t.Description} },
			nav:        kit.ListNav{MaxVisible: 10},
		},
	}
}

// EnterSelect activates the selector and loads every tool with its per-level
// enabled state.
func (s *ToolSelector) EnterSelect(width, height int, mcpTools func() []core.ToolSchema) error {
	allTools := coretool.GetToolSchemasWith(coretool.SchemaOptions{MCPTools: mcpTools})

	// Pre-load both levels so switching tabs never has to touch disk.
	s.disabledByLevel = map[bool]map[string]bool{
		false: cloneDisabled(s.loadDisabled(false)),
		true:  cloneDisabled(s.loadDisabled(true)),
	}

	items := make([]toolItem, 0, len(allTools))
	for _, t := range allTools {
		items = append(items, toolItem{Name: t.Name, Description: t.Description})
	}

	s.list.load(items, width, height)
	s.recomputeEnabled()
	return nil
}

// cloneDisabled returns a mutable copy of a level's disabled map, never nil so
// Toggle can write into it.
func cloneDisabled(m map[string]bool) map[string]bool {
	if c := maps.Clone(m); c != nil {
		return c
	}
	return map[string]bool{}
}

func (s *ToolSelector) IsActive() bool { return s.list.active }

func (s *ToolSelector) saveLevelForActiveTab() bool {
	return s.list.activeTab == int(toolTabUser)
}

// effectiveDisabled resolves a tool's state at the given level: an explicit
// settings entry wins; absent keys fall back to the factory default.
func (s *ToolSelector) effectiveDisabled(userLevel bool, name string) bool {
	if disabled, ok := s.disabledByLevel[userLevel][name]; ok {
		return disabled
	}
	return setting.IsDefaultDisabledTool(name)
}

// recomputeEnabled refreshes every item's Enabled flag for the active tab's
// level. Called after a tab switch, since a tool's state differs per level.
func (s *ToolSelector) recomputeEnabled() {
	userLevel := s.saveLevelForActiveTab()
	for i := range s.list.items {
		s.list.items[i].Enabled = !s.effectiveDisabled(userLevel, s.list.items[i].Name)
	}
	for i := range s.list.filtered {
		s.list.filtered[i].Enabled = !s.effectiveDisabled(userLevel, s.list.filtered[i].Name)
	}
}

// Toggle flips the enabled state of the selected tool at the active level and
// persists it.
func (s *ToolSelector) Toggle() tea.Cmd {
	if len(s.list.filtered) == 0 || s.list.nav.Selected >= len(s.list.filtered) {
		return nil
	}
	userLevel := s.saveLevelForActiveTab()

	selected := &s.list.filtered[s.list.nav.Selected]
	selected.Enabled = !selected.Enabled
	for i := range s.list.items {
		if s.list.items[i].Name == selected.Name {
			s.list.items[i].Enabled = selected.Enabled
			break
		}
	}

	// A factory-default-disabled tool needs an explicit "false" entry when
	// enabled — deleting its key would fall back to the disabled default.
	m := s.disabledByLevel[userLevel]
	switch {
	case selected.Enabled && setting.IsDefaultDisabledTool(selected.Name):
		m[selected.Name] = false
	case selected.Enabled:
		delete(m, selected.Name)
	case setting.IsDefaultDisabledTool(selected.Name):
		delete(m, selected.Name)
	default:
		m[selected.Name] = true
	}
	_ = s.saveDisabled(m, userLevel)

	return func() tea.Msg {
		return ToolToggleMsg{ToolName: selected.Name, Enabled: selected.Enabled}
	}
}

func (s *ToolSelector) HandleKeypress(key tea.KeyMsg) tea.Cmd {
	prevTab := s.list.activeTab
	cmd := s.list.handleKey(key, s.Toggle)
	// A tab switch changes the level, so re-resolve every tool's shown state.
	if s.list.activeTab != prevTab {
		s.recomputeEnabled()
	}
	return cmd
}

// ── Rendering ──────────────────────────────────────────────────────────────────

func (s *ToolSelector) Render() string {
	return s.list.render(s.renderItemList)
}

func (s *ToolSelector) renderItemList(sb *strings.Builder, panel kit.Panel) {
	startIdx, endIdx := s.list.nav.VisibleRange()

	if startIdx > 0 {
		sb.WriteString(kit.MoreAbove())
		sb.WriteString("\n")
	}

	// Display-width column: a CJK tool name renders at twice its byte-count.
	maxNameLen := 12
	for i := startIdx; i < endIdx; i++ {
		if w := lipgloss.Width(s.list.filtered[i].Name); w > maxNameLen {
			maxNameLen = w
		}
	}
	maxNameLen = min(maxNameLen, 24)

	descStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)

	for i := startIdx; i < endIdx; i++ {
		t := s.list.filtered[i]

		var statusIcon string
		var statusStyle lipgloss.Style
		if t.Enabled {
			statusIcon = "●"
			statusStyle = kit.SelectorStatusConnected()
		} else {
			statusIcon = "○"
			statusStyle = kit.SelectorStatusNone()
		}

		name := kit.TruncateText(t.Name, maxNameLen)
		paddedName := name + strings.Repeat(" ", max(0, maxNameLen-lipgloss.Width(name)))

		// Tool descriptions can be multi-paragraph; the row shows the first line.
		desc := t.Description
		if idx := strings.IndexByte(desc, '\n'); idx != -1 {
			desc = desc[:idx]
		}

		// Width budget for one row, accounting for the panel's Padding(1, 2)
		// (4 cols total) plus the row's own decoration:
		//   2 ("> ") + 1 (icon) + 1 (space) + name + 2 (sep) + desc
		// The trailing -4 is a right-margin safety buffer.
		rowFixed := 2 + 1 + 1 + maxNameLen + 2
		descWidth := max(15, panel.ContentWidth()-4-rowFixed-4)
		desc = kit.TruncateText(desc, descWidth)

		line := fmt.Sprintf("%s %s  %s",
			statusStyle.Render(statusIcon),
			paddedName,
			descStyle.Render(desc),
		)

		// Render without the row style's PaddingLeft(2) so the left edge lines
		// up with tabs/search/separator; Width right-pads to the inner content
		// area so the right edge matches the separator line too.
		rowWidth := max(20, panel.ContentWidth()-4)
		sb.WriteString(kit.RenderPanelRow(line, i == s.list.nav.Selected, rowWidth))
		sb.WriteString("\n")

		// Spacer for breathing room between rows (matches agent/skill).
		if i < endIdx-1 {
			sb.WriteString("\n")
		}
	}

	if endIdx < len(s.list.filtered) {
		sb.WriteString(kit.MoreBelow())
		sb.WriteString("\n")
	}
}
