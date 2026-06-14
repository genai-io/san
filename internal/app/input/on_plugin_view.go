package input

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/kit"
)

func (s *PluginSelector) Render() string {
	if !s.active {
		return ""
	}

	switch s.level {
	case pluginLevelDetail:
		if s.detailPlugin != nil {
			return s.renderInstalledDetail()
		}
		if s.detailDiscover != nil {
			return s.renderDiscoverDetail()
		}
		if s.detailMarketplace != nil {
			return s.renderMarketplaceDetail()
		}
	case pluginLevelAddMarketplace:
		return s.renderAddMarketplaceDialog()
	case pluginLevelBrowsePlugins:
		return s.renderBrowsePlugins()
	}

	return s.renderTabList()
}

func (s *PluginSelector) boxWidth() int {
	return max(60, s.width-6)
}

func (s *PluginSelector) boxHeight() int {
	return max(18, s.height-4)
}

func (s *PluginSelector) contentWidth() int {
	return s.boxWidth() - 4 // padding(1,2) takes 4 chars
}

func (s *PluginSelector) bodyHeight() int {
	return max(6, s.boxHeight()-10)
}

func (s *PluginSelector) sepLine() string {
	sepStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim)
	return sepStyle.Render(strings.Repeat("─", s.contentWidth()-4))
}

// ── Full-width placement ──────────────────────────────────────────────────

func (s *PluginSelector) renderFullWidth(content string) string {
	box := lipgloss.NewStyle().
		Width(s.boxWidth()).
		Height(s.boxHeight()).
		Padding(1, 2).
		Render(content)
	return lipgloss.Place(s.width, s.height-2, lipgloss.Center, lipgloss.Top, box)
}

// ── Tabs ──────────────────────────────────────────────────────────────────

func (s *PluginSelector) renderTabs() string {
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
		tab  pluginTab
	}{
		{"Discover", pluginTabDiscover},
		{"Installed", pluginTabInstalled},
		{"Marketplaces", pluginTabMarketplaces},
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

// ── Search box ────────────────────────────────────────────────────────────

func (s *PluginSelector) renderSearchBox(sb *strings.Builder) {
	innerWidth := max(20, s.contentWidth()-4)

	var text string
	if s.searchQuery != "" {
		pos, total := s.getItemCount()
		text = fmt.Sprintf(" %s▏ (%d/%d)", s.searchQuery, pos, total)
	} else {
		switch s.activeTab {
		case pluginTabDiscover:
			text = " Type to filter plugins..."
		case pluginTabInstalled:
			text = " Type to filter installed..."
		case pluginTabMarketplaces:
			text = " Type to filter marketplaces..."
		}
	}

	textFg := kit.CurrentTheme.TextDim
	if s.searchQuery != "" {
		textFg = kit.CurrentTheme.Text
	}

	sb.WriteString(lipgloss.NewStyle().
		Foreground(textFg).
		Background(kit.SearchBg).
		Padding(0, 1).
		Width(innerWidth).
		Render(text))
}

// ── Tab list (main view) ──────────────────────────────────────────────────

func (s *PluginSelector) renderTabList() string {
	var sb strings.Builder

	// Separator above tabs
	sb.WriteString(s.sepLine())
	sb.WriteString("\n")

	// Tab header
	sb.WriteString(s.renderTabs())
	sb.WriteString("\n\n")

	// Search box
	s.renderSearchBox(&sb)
	sb.WriteString("\n\n")

	// Tab content
	var body strings.Builder
	switch s.activeTab {
	case pluginTabInstalled:
		s.renderInstalledList(&body)
	case pluginTabDiscover:
		s.renderDiscoverList(&body)
	case pluginTabMarketplaces:
		s.renderMarketplacesList(&body)
	}
	sb.WriteString(s.renderViewport(body.String(), 0))

	// Footer
	sb.WriteString("\n")
	sb.WriteString(s.sepLine())
	sb.WriteString("\n")
	s.renderFooter(&sb, s.getTabHint())

	return s.renderFullWidth(sb.String())
}

func (s *PluginSelector) getItemCount() (int, int) {
	total := len(s.filteredItems)
	if s.activeTab == pluginTabMarketplaces {
		total++
	}
	pos := s.selectedIdx + 1
	if total == 0 {
		pos = 0
	}
	return pos, total
}

func (s *PluginSelector) getTabHint() string {
	switch s.activeTab {
	case pluginTabInstalled:
		return "←/→ tabs · ↑/↓ navigate · space toggle · enter details · esc close"
	case pluginTabDiscover:
		return "←/→ tabs · ↑/↓ navigate · enter details · esc close"
	case pluginTabMarketplaces:
		return "←/→ tabs · ↑/↓ navigate · u update · r remove · esc close"
	}
	return ""
}

// ── Installed list ────────────────────────────────────────────────────────

func (s *PluginSelector) renderInstalledList(sb *strings.Builder) {
	dimStyle := kit.DimStyle()

	if len(s.filteredItems) == 0 {
		if len(s.installedFlatList) == 0 {
			sb.WriteString(dimStyle.PaddingLeft(2).Render("No plugins installed"))
			sb.WriteString("\n\n")
			sb.WriteString(dimStyle.PaddingLeft(2).Render("Run: san plugin install <name>@<marketplace>"))
			sb.WriteString("\n")
		} else {
			sb.WriteString(dimStyle.PaddingLeft(2).Render("No plugins match the filter"))
			sb.WriteString("\n")
		}
		return
	}

	visible := max(4, s.bodyHeight())
	endIdx := min(s.scrollOffset+visible, len(s.filteredItems))
	cw := s.contentWidth()

	if s.scrollOffset > 0 {
		sb.WriteString(dimStyle.PaddingLeft(2).Render("↑ more above"))
		sb.WriteString("\n")
	}

	for i := s.scrollOffset; i < endIdx; i++ {
		p, ok := s.filteredItems[i].(pluginItem)
		if !ok {
			continue
		}

		icon, iconStyle := pluginStatusIconAndStyle(p.Enabled)

		nameStr := p.Name
		if p.Marketplace != "" {
			nameStr += dimStyle.Render(" · " + p.Marketplace)
		}

		line := fmt.Sprintf("%s %s", iconStyle.Render(icon), nameStr)

		if p.Description != "" {
			// 4 = PaddingLeft(2) + cursor(2) from RenderSelectableRow; 3 = " · " separator
			maxDescLen := cw - lipgloss.Width(line) - 4 - 3
			if maxDescLen > 20 {
				desc := kit.TruncateText(p.Description, maxDescLen)
				line += dimStyle.Render(" · " + desc)
			}
		}

		sb.WriteString(kit.RenderSelectableRow(line, i == s.selectedIdx))
		sb.WriteString("\n")
	}

	if endIdx < len(s.filteredItems) {
		sb.WriteString(dimStyle.PaddingLeft(2).Render("↓ more below"))
		sb.WriteString("\n")
	}
}

// ── Discover list ─────────────────────────────────────────────────────────

func (s *PluginSelector) renderDiscoverList(sb *strings.Builder) {
	dimStyle := kit.DimStyle()

	if len(s.filteredItems) == 0 {
		if len(s.discoverPlugins) == 0 {
			sb.WriteString(dimStyle.PaddingLeft(2).Render("No plugins available"))
			sb.WriteString("\n\n")
			sb.WriteString(dimStyle.PaddingLeft(2).Render("Add a marketplace in the Marketplaces tab"))
			sb.WriteString("\n")
		} else {
			sb.WriteString(dimStyle.PaddingLeft(2).Render("No plugins match the filter"))
			sb.WriteString("\n")
		}
		return
	}

	maxItems := max(3, s.bodyHeight()/3)
	endIdx := min(s.scrollOffset+maxItems, len(s.filteredItems))

	if s.scrollOffset > 0 {
		sb.WriteString(dimStyle.PaddingLeft(2).Render("↑ more above"))
		sb.WriteString("\n")
	}

	cw := s.contentWidth()
	for i := s.scrollOffset; i < endIdx; i++ {
		p, ok := s.filteredItems[i].(pluginDiscoverItem)
		if !ok {
			continue
		}

		icon := "○"
		iconStyle := dimStyle
		if p.Installed {
			icon = "●"
			iconStyle = kit.SelectorStatusConnected()
		}

		line := fmt.Sprintf("%s %s%s", iconStyle.Render(icon), p.Name, dimStyle.Render(" · "+p.Marketplace))
		sb.WriteString(kit.RenderSelectableRow(line, i == s.selectedIdx))
		sb.WriteString("\n")

		if p.Description != "" {
			maxDescLen := cw - 8
			if maxDescLen > 100 {
				maxDescLen = 100
			}
			if maxDescLen > 20 {
				desc := kit.TruncateText(p.Description, maxDescLen)
				sb.WriteString(dimStyle.PaddingLeft(6).Render(desc))
				sb.WriteString("\n")
			}
		}

		sb.WriteString("\n")
	}

	remaining := len(s.filteredItems) - endIdx
	if remaining > 0 {
		sb.WriteString(dimStyle.PaddingLeft(2).Render(fmt.Sprintf("↓ %d more below", remaining)))
		sb.WriteString("\n")
	}
}

// ── Marketplaces list ─────────────────────────────────────────────────────

func (s *PluginSelector) renderMarketplacesList(sb *strings.Builder) {
	dimStyle := kit.DimStyle()
	addStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Success).Bold(true)

	addLine := addStyle.Render("+ Add Marketplace")
	sb.WriteString(kit.RenderSelectableRow(addLine, s.selectedIdx == 0))
	sb.WriteString("\n")

	if len(s.filteredItems) == 0 {
		sb.WriteString("\n")
		sb.WriteString(dimStyle.PaddingLeft(2).Render("No marketplaces configured"))
		sb.WriteString("\n")
		return
	}

	sb.WriteString("\n")

	visible := max(4, s.bodyHeight()/2)
	endIdx := min(s.scrollOffset+visible, len(s.filteredItems))

	for i := s.scrollOffset; i < endIdx; i++ {
		m, ok := s.filteredItems[i].(pluginMarketplaceItem)
		if !ok {
			continue
		}

		displayIdx := i + 1
		official := ""
		if m.IsOfficial {
			official = " ★"
		}

		line := fmt.Sprintf("%s %s%s", kit.SelectorStatusConnected().Render("●"), m.ID, dimStyle.Render(official))
		sb.WriteString(kit.RenderSelectableRow(line, displayIdx == s.selectedIdx))
		sb.WriteString("\n")

		if displayIdx == s.selectedIdx {
			sb.WriteString(dimStyle.PaddingLeft(6).Render(m.Source))
			sb.WriteString("\n")
			stats := fmt.Sprintf("%d available · %d installed", m.Available, m.Installed)
			if m.LastUpdated != "" {
				stats += " · " + m.LastUpdated
			}
			sb.WriteString(dimStyle.PaddingLeft(6).Render(stats))
			sb.WriteString("\n")
		}
	}
}

// ── Detail views ──────────────────────────────────────────────────────────

func (s *PluginSelector) renderInstalledDetail() string {
	if s.detailPlugin == nil {
		return s.renderTabList()
	}

	var sb strings.Builder
	p := s.detailPlugin
	cw := s.contentWidth()

	dimStyle := kit.DimStyle()
	brightStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextBright)
	labelStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted).Width(12)

	// Header
	sb.WriteString(s.sepLine())
	sb.WriteString("\n")
	sb.WriteString(kit.SelectorTitleStyle().Render("Plugin Details"))
	sb.WriteString("\n")
	sb.WriteString(dimStyle.Render("> " + p.FullName))
	sb.WriteString("\n\n")

	icon, iconStyle := pluginStatusIconAndStyle(p.Enabled)
	statusLabel := "Disabled"
	if p.Enabled {
		statusLabel = "Enabled"
	}
	sb.WriteString(fmt.Sprintf("  %s %s\n", labelStyle.Render("Status:"), iconStyle.Render(icon+" "+statusLabel)))
	sb.WriteString(fmt.Sprintf("  %s %s\n", labelStyle.Render("Scope:"), brightStyle.Render(string(p.Scope))))

	if p.Version != "" {
		sb.WriteString(fmt.Sprintf("  %s %s\n", labelStyle.Render("Version:"), brightStyle.Render(p.Version)))
	}

	if p.Author != "" {
		sb.WriteString(fmt.Sprintf("  %s %s\n", labelStyle.Render("Author:"), brightStyle.Render(p.Author)))
	}

	if p.Description != "" {
		sb.WriteString("\n")
		desc := kit.TruncateText(p.Description, cw-4)
		sb.WriteString("  " + dimStyle.Render(desc))
		sb.WriteString("\n")
	}

	components := pluginBuildComponentList(p)
	if len(components) > 0 {
		sb.WriteString("\n")
		compLabel := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Text).Bold(true)
		sb.WriteString("  " + compLabel.Render("Components"))
		sb.WriteString("\n")
		for _, c := range components {
			sb.WriteString("  " + dimStyle.Render("  • "+c))
			sb.WriteString("\n")
		}
	}

	if len(p.Errors) > 0 {
		sb.WriteString("\n")
		sb.WriteString("  " + kit.SelectorStatusError().Render("Errors"))
		sb.WriteString("\n")
		maxValueLen := cw - 8
		for _, err := range p.Errors {
			sb.WriteString("  " + kit.SelectorStatusError().Render("  • "+kit.TruncateText(err, maxValueLen)))
			sb.WriteString("\n")
		}
	}

	s.renderActions(&sb)

	sb.WriteString("\n")
	sb.WriteString(s.sepLine())
	sb.WriteString("\n")
	s.renderFooter(&sb, "↑/↓ scroll/actions · enter select · esc back")

	return s.renderFullWidth(sb.String())
}

func (s *PluginSelector) renderDiscoverDetail() string {
	if s.detailDiscover == nil {
		return s.renderTabList()
	}

	var sb strings.Builder
	p := s.detailDiscover
	cw := s.contentWidth()

	dimStyle := kit.DimStyle()
	brightStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextBright)
	warnStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Warning)

	// Header
	sb.WriteString(s.sepLine())
	sb.WriteString("\n")
	sb.WriteString(kit.SelectorTitleStyle().Render("Install Plugin"))
	sb.WriteString("\n")
	sb.WriteString(dimStyle.Render("> " + p.Name + "@" + p.Marketplace))
	sb.WriteString("\n\n")

	sb.WriteString(brightStyle.Render(p.Name))
	sb.WriteString("\n")
	sb.WriteString(dimStyle.Render("from " + p.Marketplace))
	sb.WriteString("\n\n")

	if p.Description != "" {
		desc := kit.TruncateText(p.Description, cw-4)
		sb.WriteString("  " + dimStyle.Render(desc))
		sb.WriteString("\n\n")
	}

	if p.Author != "" {
		sb.WriteString("  " + dimStyle.Render("By: "))
		sb.WriteString(brightStyle.Render(p.Author))
		sb.WriteString("\n\n")
	}

	sb.WriteString("  " + warnStyle.Render("⚠ Make sure you trust a plugin before installing"))
	sb.WriteString("\n")

	s.renderActions(&sb)

	sb.WriteString("\n")
	sb.WriteString(s.sepLine())
	sb.WriteString("\n")
	s.renderFooter(&sb, "↑/↓ scroll/actions · enter select · esc back")

	return s.renderFullWidth(sb.String())
}

func (s *PluginSelector) renderMarketplaceDetail() string {
	if s.detailMarketplace == nil {
		return s.renderTabList()
	}

	var sb strings.Builder
	m := s.detailMarketplace

	dimStyle := kit.DimStyle()
	brightStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextBright)

	// Header
	sb.WriteString(s.sepLine())
	sb.WriteString("\n")
	sb.WriteString(kit.SelectorTitleStyle().Render("Marketplace Details"))
	sb.WriteString("\n")
	sb.WriteString(dimStyle.Render("> " + m.ID))
	sb.WriteString("\n\n")

	sb.WriteString(brightStyle.Render(m.ID))
	sb.WriteString("\n")
	sb.WriteString(dimStyle.Render(m.Source))
	sb.WriteString("\n\n")

	sb.WriteString(fmt.Sprintf("  %d available plugins", m.Available))
	sb.WriteString("\n")

	if m.Installed > 0 {
		sb.WriteString("\n")
		sb.WriteString("  " + dimStyle.Render(fmt.Sprintf("Installed (%d):", m.Installed)))
		sb.WriteString("\n")
		for _, p := range s.registry.List() {
			if idx := strings.Index(p.Source, "@"); idx != -1 && p.Source[idx+1:] == m.ID {
				sb.WriteString("    " + kit.SelectorStatusConnected().Render("●") + " " + p.Name())
				sb.WriteString("\n")
			}
		}
	}

	s.renderActions(&sb)

	sb.WriteString("\n")
	sb.WriteString(s.sepLine())
	sb.WriteString("\n")
	s.renderFooter(&sb, "↑/↓ scroll/actions · enter select · esc back")

	return s.renderFullWidth(sb.String())
}

// ── Add marketplace dialog ────────────────────────────────────────────────

func (s *PluginSelector) renderAddMarketplaceDialog() string {
	var sb strings.Builder
	cw := s.contentWidth()

	dimStyle := kit.DimStyle()
	brightStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextBright)

	sb.WriteString(s.sepLine())
	sb.WriteString("\n")
	sb.WriteString(kit.SelectorTitleStyle().Render("Add Marketplace"))
	sb.WriteString("\n\n")

	sb.WriteString(dimStyle.Render("Enter marketplace source:"))
	sb.WriteString("\n\n")
	sb.WriteString(dimStyle.Render("Examples:"))
	sb.WriteString("\n")
	sb.WriteString(dimStyle.Render("  • https://github.com/owner/repo"))
	sb.WriteString("\n")
	sb.WriteString(dimStyle.Render("  • owner/repo (GitHub shorthand)"))
	sb.WriteString("\n")
	sb.WriteString(dimStyle.Render("  • ./path/to/marketplace (local)"))
	sb.WriteString("\n\n")

	maxInputLen := cw - 6
	inputLine := s.addMarketplaceInput + "│"
	if len(inputLine) > maxInputLen {
		inputLine = "…" + inputLine[len(inputLine)-maxInputLen+1:]
	}
	sb.WriteString(brightStyle.Render("> " + inputLine))
	sb.WriteString("\n")

	sb.WriteString("\n")
	sb.WriteString(s.sepLine())
	sb.WriteString("\n")
	s.renderFooter(&sb, "enter add · esc cancel")

	return s.renderFullWidth(sb.String())
}

// ── Browse plugins ────────────────────────────────────────────────────────

func (s *PluginSelector) renderBrowsePlugins() string {
	var sb strings.Builder
	dimStyle := kit.DimStyle()
	brightStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextBright)

	sb.WriteString(s.sepLine())
	sb.WriteString("\n")
	sb.WriteString(kit.SelectorTitleStyle().Render("Browse Marketplace"))
	sb.WriteString("\n")
	sb.WriteString(dimStyle.Render("> " + s.browseMarketplaceID))
	sb.WriteString("\n\n")

	sb.WriteString(brightStyle.Render(s.browseMarketplaceID))
	sb.WriteString("\n")
	sb.WriteString(dimStyle.Render(fmt.Sprintf("%d available plugins", len(s.browsePlugins))))
	sb.WriteString("\n\n")

	cw := s.contentWidth()
	if len(s.browsePlugins) == 0 {
		sb.WriteString(dimStyle.PaddingLeft(2).Render("No plugins found"))
		sb.WriteString("\n")
	} else {
		visible := max(4, s.bodyHeight())
		endIdx := min(s.scrollOffset+visible, len(s.browsePlugins))

		if s.scrollOffset > 0 {
			sb.WriteString(dimStyle.PaddingLeft(2).Render("↑ more above"))
			sb.WriteString("\n")
		}

		for i := s.scrollOffset; i < endIdx; i++ {
			p := s.browsePlugins[i]

			icon := "○"
			iconStyle := dimStyle
			if p.Installed {
				icon = "●"
				iconStyle = kit.SelectorStatusConnected()
			}

			line := fmt.Sprintf("%s %s", iconStyle.Render(icon), p.Name)
			sb.WriteString(kit.RenderSelectableRow(line, i == s.selectedIdx))
			sb.WriteString("\n")

			if p.Description != "" && i == s.selectedIdx {
				desc := kit.TruncateText(p.Description, cw-10)
				sb.WriteString(dimStyle.PaddingLeft(6).Render(desc))
				sb.WriteString("\n")
			}
		}

		if endIdx < len(s.browsePlugins) {
			sb.WriteString(dimStyle.PaddingLeft(2).Render("↓ more below"))
			sb.WriteString("\n")
		}
	}

	sb.WriteString("\n")
	sb.WriteString(s.sepLine())
	sb.WriteString("\n")
	s.renderFooter(&sb, "↑/↓ navigate · enter details · esc back")

	return s.renderFullWidth(sb.String())
}

// ── Actions ───────────────────────────────────────────────────────────────

func (s *PluginSelector) renderActions(sb *strings.Builder) {
	sb.WriteString("\n")
	accentStyle := lipgloss.NewStyle().
		Foreground(kit.CurrentTheme.Primary).
		Bold(true).
		PaddingLeft(2)
	normalStyle := lipgloss.NewStyle().
		Foreground(kit.CurrentTheme.Text).
		PaddingLeft(2)

	for i, action := range s.actions {
		if i == s.actionIdx {
			sb.WriteString(accentStyle.Render("❯ " + action.Label))
		} else {
			sb.WriteString(normalStyle.Render("  " + action.Label))
		}
		sb.WriteString("\n")
	}
}

// ── Footer ────────────────────────────────────────────────────────────────

var pluginSpinnerFrames = [4]string{"◐", "◓", "◑", "◒"}

func (s *PluginSelector) renderFooter(sb *strings.Builder, hint string) {
	if s.isLoading {
		spinnerStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Accent)
		frame := pluginSpinnerFrames[s.loadingFrame%len(pluginSpinnerFrames)]
		sb.WriteString(spinnerStyle.Render("  " + frame + " " + s.loadingMsg))
		sb.WriteString("\n")
	} else if s.lastMessage != "" {
		if s.isError {
			sb.WriteString(kit.SelectorStatusError().Render("  ⚠ " + s.lastMessage))
		} else {
			successStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Success)
			sb.WriteString(successStyle.Render("  ✓ " + s.lastMessage))
		}
		sb.WriteString("\n")
	}
	sb.WriteString(kit.DimStyle().Render(hint))
}

// ── Viewport ──────────────────────────────────────────────────────────────

func (s *PluginSelector) renderViewport(content string, scroll int) string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}

	visible := s.bodyHeight()
	if visible <= 0 {
		return ""
	}
	if scroll < 0 {
		scroll = 0
	}
	maxScroll := max(0, len(lines)-visible)
	if scroll > maxScroll {
		scroll = maxScroll
	}

	end := min(len(lines), scroll+visible)
	view := lines
	if len(lines) > 0 {
		view = lines[scroll:end]
	}

	if len(view) < visible {
		for len(view) < visible {
			view = append(view, "")
		}
	}

	return strings.Join(view, "\n") + "\n"
}

// ── Helpers ───────────────────────────────────────────────────────────────

func pluginStatusIconAndStyle(enabled bool) (string, lipgloss.Style) {
	if enabled {
		return "●", kit.SelectorStatusConnected()
	}
	return "○", kit.SelectorStatusNone()
}

func pluginBuildComponentList(p *pluginItem) []string {
	type componentCount struct {
		icon  string
		name  string
		count int
	}
	counts := []componentCount{
		{"✦", "Skills", p.Skills},
		{"⚑", "Agents", p.Agents},
		{"⌘", "Commands", p.Commands},
		{"↪", "Hooks", p.Hooks},
		{"☉", "MCP Servers", p.MCP},
		{"⦾", "LSP Servers", p.LSP},
	}

	var result []string
	for _, c := range counts {
		if c.count > 0 {
			result = append(result, fmt.Sprintf("%s %s: %d", c.icon, c.name, c.count))
		}
	}
	return result
}
