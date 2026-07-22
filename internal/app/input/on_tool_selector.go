package input

import (
	"fmt"
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
//
// A tool's shown state is derived on the fly from disabledByLevel + the active
// tab rather than cached on the item: because every tool appears under both
// level tabs, a cached flag would go stale on every tab switch.
type ToolSelector struct {
	list         tabbedList[toolItem]
	loadDisabled func(userLevel bool) map[string]bool
	saveDisabled func(disabled map[string]bool, userLevel bool) error
	// disabledByLevel holds each level's explicit disabled entries (keyed by
	// userLevel), loaded once per open and mutated by Toggle.
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

// EnterSelect activates the selector and loads every tool.
func (s *ToolSelector) EnterSelect(width, height int, mcpTools func() []core.ToolSchema) error {
	allTools := coretool.GetToolSchemasWith(coretool.SchemaOptions{MCPTools: mcpTools})

	// Pre-load both levels so switching tabs never has to touch disk. The loader
	// hands back a fresh, owned map per level, so no copy is needed.
	s.disabledByLevel = map[bool]map[string]bool{
		false: s.loadDisabled(false),
		true:  s.loadDisabled(true),
	}

	items := make([]toolItem, 0, len(allTools))
	for _, t := range allTools {
		items = append(items, toolItem{Name: t.Name, Description: t.Description})
	}

	s.list.load(items, width, height)
	return nil
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

// Toggle flips the enabled state of the selected tool at the active level and
// persists it.
func (s *ToolSelector) Toggle() tea.Cmd {
	if len(s.list.filtered) == 0 || s.list.nav.Selected >= len(s.list.filtered) {
		return nil
	}
	userLevel := s.saveLevelForActiveTab()
	name := s.list.filtered[s.list.nav.Selected].Name
	// A currently-disabled tool is being enabled, and vice versa.
	enabling := s.effectiveDisabled(userLevel, name)

	// A factory-default-disabled tool needs an explicit "false" entry when
	// enabled — deleting its key would fall back to the disabled default.
	m := s.disabledByLevel[userLevel]
	switch {
	case enabling && setting.IsDefaultDisabledTool(name):
		m[name] = false
	case enabling:
		delete(m, name)
	case setting.IsDefaultDisabledTool(name):
		delete(m, name)
	default:
		m[name] = true
	}
	_ = s.saveDisabled(m, userLevel)

	return func() tea.Msg {
		return ToolToggleMsg{ToolName: name, Enabled: enabling}
	}
}

func (s *ToolSelector) HandleKeypress(key tea.KeyMsg) tea.Cmd {
	return s.list.handleKey(key, s.Toggle)
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

	userLevel := s.saveLevelForActiveTab()
	descStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)

	for i := startIdx; i < endIdx; i++ {
		t := s.list.filtered[i]

		var statusIcon string
		var statusStyle lipgloss.Style
		if s.effectiveDisabled(userLevel, t.Name) {
			statusIcon = "○"
			statusStyle = kit.SelectorStatusNone()
		} else {
			statusIcon = "●"
			statusStyle = kit.SelectorStatusConnected()
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
