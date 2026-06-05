package input

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/llm"
)

// Render renders the unified model & provider selector as a full-screen overlay.
func (s *ProviderSelector) Render() string {
	if !s.active {
		return ""
	}

	if len(s.visibleItems) == 0 && len(s.allModels) == 0 && len(s.allProviders) == 0 {
		return s.renderEmptyState()
	}

	var sb strings.Builder

	// Separator above tabs
	sb.WriteString(s.sepLine())
	sb.WriteString("\n")

	// Tab header
	sb.WriteString(s.renderTabs())
	sb.WriteString("\n\n")

	// Search box
	sb.WriteString(s.renderSearchBox())
	sb.WriteString("\n\n")

	var body strings.Builder
	if len(s.visibleItems) == 0 {
		body.WriteString(s.emptyFilterMsg())
		body.WriteString("\n")
	} else {
		s.renderItemList(&body)
	}
	sb.WriteString(s.renderViewport(body.String()))

	// Separator before hints
	sb.WriteString("\n")
	sb.WriteString(s.sepLine())
	sb.WriteString("\n")
	sb.WriteString(s.renderHints())

	content := sb.String()
	cw := s.contentWidth()
	box := lipgloss.NewStyle().
		Width(cw).
		Height(s.boxHeight()).
		Padding(1, 2).
		Render(content)
	return lipgloss.Place(s.width, s.height-2, lipgloss.Center, lipgloss.Top, box)
}

// contentWidth returns the usable width for the panel content.
func (s *ProviderSelector) contentWidth() int {
	// Use full terminal width minus a small margin
	return max(60, s.width-6)
}

// boxHeight returns the fixed height for the panel, consistent across tabs.
func (s *ProviderSelector) boxHeight() int {
	return max(18, s.height-4)
}

// bodyHeight returns the fixed height for the scrollable content area.
func (s *ProviderSelector) bodyHeight() int {
	return max(6, s.boxHeight()-10)
}

func (s *ProviderSelector) renderViewport(content string) string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}

	visible := s.bodyHeight()
	if visible <= 0 {
		return ""
	}

	view := lines
	for len(view) < visible {
		view = append(view, "")
	}

	return strings.Join(view, "\n") + "\n"
}

func (s *ProviderSelector) sepLine() string {
	sepStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim)
	return sepStyle.Render(strings.Repeat("─", s.contentWidth()-8))
}

// emptyFilterMsg returns the "no matches" text for the current tab.
func (s *ProviderSelector) emptyFilterMsg() string {
	if s.activeTab == providerTabModels {
		return kit.DimStyle().PaddingLeft(2).Render("No models match the filter")
	}
	return kit.DimStyle().PaddingLeft(2).Render("No providers match the filter")
}

// renderItemList renders the scrollable item list into the builder.
func (s *ProviderSelector) renderItemList(sb *strings.Builder) {
	endIdx := min(s.scrollOffset+s.maxVisible, len(s.visibleItems))

	if s.scrollOffset > 0 {
		sb.WriteString(kit.DimStyle().PaddingLeft(2).Render("↑ more above"))
		sb.WriteString("\n")
	}

	for i := s.scrollOffset; i < endIdx; i++ {
		item := s.visibleItems[i]
		isSelected := i == s.selectedIdx

		switch item.Kind {
		case providerItemProviderHeader:
			sb.WriteString(s.renderProviderHeader(item))
		case providerItemModel:
			sb.WriteString(s.renderModelRow(item, isSelected))
		case providerItemProvider:
			sb.WriteString(s.renderProviderRow(item, isSelected, i))
		case providerItemAuthMethod:
			sb.WriteString(s.renderAuthMethod(item, isSelected, i))
		}
		sb.WriteString("\n")

		// Inline API key input (render below the relevant item)
		if s.apiKeyActive && isSelected {
			sb.WriteString(s.renderAPIKeyInput())
			sb.WriteString("\n")
		}
	}

	if endIdx < len(s.visibleItems) {
		sb.WriteString(kit.DimStyle().PaddingLeft(2).Render("↓ more below"))
		sb.WriteString("\n")
	}
}

// ── Tab header ──────────────────────────────────────────────────────────────

func (s *ProviderSelector) renderTabs() string {
	activeStyle := lipgloss.NewStyle().
		Foreground(kit.TabActiveFg).
		Background(kit.TabActiveBg).
		Bold(true).
		Padding(0, 2)
	inactiveStyle := lipgloss.NewStyle().
		Foreground(kit.CurrentTheme.TextDim).
		Padding(0, 2)

	tabs := []struct {
		name string
		tab  providerTab
	}{
		{"Models", providerTabModels},
		{"Providers", providerTabProviders},
	}

	var parts []string
	for _, t := range tabs {
		if t.tab == s.activeTab {
			parts = append(parts, activeStyle.Render(t.name))
		} else {
			parts = append(parts, inactiveStyle.Render(t.name))
		}
	}

	return strings.Join(parts, "  ")
}

// ── Search box ──────────────────────────────────────────────────────────────

func (s *ProviderSelector) renderSearchBox() string {
	innerWidth := max(20, s.contentWidth()-8)

	var text string
	if s.activeTab == providerTabModels && s.searchQuery != "" {
		totalModels := len(s.allModels)
		filteredCount := len(s.filteredModels)
		text = fmt.Sprintf(" 🔍 %s▏ (%d/%d)", s.searchQuery, filteredCount, totalModels)
	} else if s.searchQuery != "" {
		text = " 🔍 " + s.searchQuery + "▏"
	} else {
		if s.activeTab == providerTabProviders {
			text = " 🔍 Type to filter providers..."
		} else {
			text = " 🔍 Type to filter models..."
		}
	}

	textFg := kit.CurrentTheme.TextDim
	if s.searchQuery != "" {
		textFg = kit.CurrentTheme.Text
	}

	searchBg := kit.SearchBg
	return lipgloss.NewStyle().
		Foreground(textFg).
		Background(searchBg).
		Padding(0, 1).
		Width(innerWidth).
		Render(text)
}

// ── Empty / no providers ────────────────────────────────────────────────────

func (s *ProviderSelector) renderEmptyState() string {
	warningStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Warning).Bold(true)
	msgStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Text)
	cmdStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Primary).Bold(true)

	content := s.renderTabs() + "\n\n" +
		s.renderSearchBox() + "\n\n" +
		warningStyle.Render("  ⚠  No Models Available") + "\n\n" +
		msgStyle.Render("  No LLM provider is connected yet.") + "\n" +
		msgStyle.Render("  Press ") + cmdStyle.Render("Tab") + msgStyle.Render(" to switch to Providers tab and connect one.") + "\n\n" +
		kit.DimStyle().Render("←/→/Tab switch · Esc cancel")

	cw := s.contentWidth()
	box := lipgloss.NewStyle().
		Width(cw).
		Height(s.boxHeight()).
		Padding(1, 2).
		Render(content)
	return lipgloss.Place(s.width, s.height-2, lipgloss.Center, lipgloss.Top, box)
}

// ── Models tab rows ─────────────────────────────────────────────────────────

func (s *ProviderSelector) renderProviderHeader(item providerListItem) string {
	style := lipgloss.NewStyle().
		Foreground(kit.CurrentTheme.TextDim).
		Bold(true)
	name := item.Provider.DisplayName
	if name == "" {
		name = string(item.Provider.Provider)
	}
	return style.Render(name)
}

func (s *ProviderSelector) renderModelRow(item providerListItem, isSelected bool) string {
	m := item.Model

	indicator := "[ ]"
	indicatorStyle := kit.SelectorStatusNone()
	if m.IsCurrent {
		indicator = "[*]"
		indicatorStyle = kit.SelectorStatusConnected()
	}

	displayName := m.DisplayName
	if displayName == "" {
		displayName = m.Name
	}
	if displayName == "" {
		displayName = m.ID
	}

	warning := ""
	if m.InputTokenLimit == 0 && m.OutputTokenLimit == 0 {
		warning = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Warning).Render(" ⚠")
	}

	line := fmt.Sprintf("%s %s%s", indicatorStyle.Render(indicator), displayName, warning)
	return kit.RenderSelectableRow(line, isSelected)
}

// ── Providers tab rows ──────────────────────────────────────────────────────

// providerNameColumnWidth is the fixed width for provider name alignment.
// Sized to fit the longest display name ("Z.ai (GLM series)", 17 cols) plus a
// comfortable gap, so every row's API-key column lines up without crowding —
// even the longest name keeps ~5 cols of breathing room before its key.
const providerNameColumnWidth = 22

func (s *ProviderSelector) renderProviderRow(item providerListItem, isSelected bool, itemIdx int) string {
	p := item.Provider
	if p == nil {
		return ""
	}

	bestStatus := providerBestAuthMethodStatus(p.AuthMethods)
	statusIcon, statusStyle, _ := providerGetStatusDisplay(bestStatus)

	envInfo := ""
	if len(p.AuthMethods) == 1 {
		envInfo = kit.RenderEnvVarStatus(providerFirstEnvVar(p.AuthMethods[0].EnvVars))
	} else if len(p.AuthMethods) > 1 {
		envInfo = kit.DimStyle().Render(fmt.Sprintf("%d auth methods", len(p.AuthMethods)))
	}
	envInfo = s.appendConnectResult(envInfo, itemIdx)

	line := kit.FormatAlignedRow(statusStyle.Render(statusIcon), p.DisplayName, providerNameColumnWidth, envInfo)
	return kit.RenderSelectableRow(line, isSelected)
}

func (s *ProviderSelector) renderAuthMethod(item providerListItem, isSelected bool, itemIdx int) string {
	am := item.AuthMethod
	if am == nil {
		return ""
	}

	statusIcon, statusStyle, statusDesc := providerGetStatusDisplay(am.Status)

	envInfo := ""
	if am.Status != llm.StatusConnected {
		envInfo = kit.RenderEnvVarStatus(providerFirstEnvVar(am.EnvVars))
	}
	if statusDesc != "" && envInfo == "" {
		envInfo = kit.DimStyle().Render(statusDesc)
	}
	envInfo = s.appendConnectResult(envInfo, itemIdx)

	colWidth := providerNameColumnWidth - 2 // sub-item indent
	line := "  " + kit.FormatAlignedRow(statusStyle.Render(statusIcon), am.DisplayName, colWidth, envInfo)
	return kit.RenderSelectableRow(line, isSelected)
}

// ── API key input ───────────────────────────────────────────────────────────

func (s *ProviderSelector) renderAPIKeyInput() string {
	label := kit.DimStyle().Render(s.apiKeyEnvVar + ": ")
	inputView := label + s.apiKeyInput.View()

	inputBg := lipgloss.AdaptiveColor{Dark: "#1E293B", Light: "#F1F5F9"}
	boxStyle := lipgloss.NewStyle().
		Background(inputBg).
		Padding(0, 1)

	// Indent to align with auth method content (6 chars: PaddingLeft(2) + "  " + "  ")
	return "      " + boxStyle.Render(inputView)
}

// ── Footer hints ────────────────────────────────────────────────────────────

func (s *ProviderSelector) renderHints() string {
	if s.apiKeyActive {
		return kit.DimStyle().Render("Paste API key · Enter confirm · Esc cancel")
	}

	var parts []string
	parts = append(parts, "↑/↓ navigate")
	if s.activeTab == providerTabProviders {
		parts = append(parts, "Enter connect/refresh")
	} else {
		parts = append(parts, "Space mark · Enter confirm")
	}
	parts = append(parts, "←/→/Tab switch", "Esc cancel")
	return kit.DimStyle().Render(strings.Join(parts, " · "))
}

// ── Connection result ───────────────────────────────────────────────────────

// appendConnectResult appends the inline connect/refresh result (an in-flight
// spinner, or e.g. "● 2 models" once done) to a row's info column when it
// belongs to itemIdx, separated from the env-var status by a gap.
func (s *ProviderSelector) appendConnectResult(envInfo string, itemIdx int) string {
	if s.lastConnectResult == "" || itemIdx != s.lastConnectAuthIdx {
		return envInfo
	}
	result := s.renderConnectResult()
	if envInfo == "" {
		return result
	}
	return envInfo + "   " + result
}

// connectResultStyle styles a completed (non-in-flight) result; the in-flight
// case is handled by renderConnectResult before this is called.
func (s *ProviderSelector) connectResultStyle() lipgloss.Style {
	if !s.lastConnectSuccess {
		return lipgloss.NewStyle().Foreground(kit.CurrentTheme.Error)
	}
	if strings.HasPrefix(s.lastConnectResult, "⚠") {
		return lipgloss.NewStyle().Foreground(kit.CurrentTheme.Warning)
	}
	// Plain success (e.g. "· 2 models") is supplementary info — the green status
	// dot and key ✓ already signal success, so keep it dim to avoid competing.
	return kit.DimStyle()
}

// providerSpinnerFrames aliases kit.BrailleSpinnerFrames so callers keep
// the existing local name; the table itself is shared (see kit/spinner.go)
// so non-Unicode TTY fallbacks land in one place.
var providerSpinnerFrames = kit.BrailleSpinnerFrames

func (s *ProviderSelector) renderConnectResult() string {
	// While in flight, show just the animated braille spinner (no text).
	if s.IsConnecting() {
		frame := providerSpinnerFrames[s.spinnerTick%len(providerSpinnerFrames)]
		return kit.DimStyle().Render(frame)
	}
	return s.connectResultStyle().Render(s.lastConnectResult)
}
