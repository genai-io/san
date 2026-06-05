package input

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/genai-io/san/v2/internal/app/kit"
	"github.com/genai-io/san/v2/internal/identity"
)

// Render draws the spacious /model-style overlay for the identity selector.
func (s *IdentitySelector) Render() string {
	if !s.active {
		return ""
	}

	panel := kit.Panel{Width: s.width, Height: s.height}

	// Each item renders on 2 lines (row + description) with a blank spacer.
	// Reserve 2 lines for more-above/more-below indicators.
	s.nav.MaxVisible = max(1, (panel.BodyHeight()-2)/3)
	s.nav.EnsureVisible()

	var sb strings.Builder
	sb.WriteString(panel.SeparatorLine())
	sb.WriteString("\n\n")
	sb.WriteString(kit.RenderSearchBox(kit.SearchBoxOpts{
		Query:       s.nav.Search,
		Placeholder: "Type to filter identities...",
		Filtered:    len(s.visible),
		Total:       len(s.items),
		Width:       panel.ContentWidth(),
	}))
	sb.WriteString("\n\n")

	var body strings.Builder
	if len(s.visible) == 0 {
		body.WriteString(kit.DimStyle().PaddingLeft(2).Render("No identities match the filter"))
		body.WriteString("\n")
	} else {
		s.renderItems(&body, panel)
	}
	sb.WriteString(panel.PadViewport(body.String()))

	sb.WriteString("\n")
	sb.WriteString(panel.SeparatorLine())
	sb.WriteString("\n")
	sb.WriteString(s.renderHints())

	return panel.Wrap(sb.String())
}

func (s *IdentitySelector) renderItems(sb *strings.Builder, panel kit.Panel) {
	start, end := s.nav.VisibleRange()

	if start > 0 {
		sb.WriteString(kit.MoreAbove())
		sb.WriteString("\n")
	}

	for i := start; i < end; i++ {
		sb.WriteString(s.renderItem(s.visible[i], i == s.nav.Selected, panel))
		sb.WriteString("\n\n")
	}

	if end < len(s.visible) {
		sb.WriteString(kit.MoreBelow())
		sb.WriteString("\n")
	}
}

func (s *IdentitySelector) renderItem(it *identity.Identity, isSelected bool, panel kit.Panel) string {
	cursor := "  "
	if isSelected {
		cursor = "> "
	}

	marker := "○"
	markerStyle := kit.SelectorStatusNone()
	isActive := it.Name == s.activeName || (s.activeName == "" && it.Name == identity.DefaultName)
	if isActive {
		marker = "●"
		markerStyle = kit.SelectorStatusConnected()
	}

	tag := ""
	switch it.Scope {
	case identity.ScopeUser:
		tag = "[user]"
	case identity.ScopeProject:
		tag = "[project]"
	}

	nameStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Text)
	if isSelected {
		nameStyle = nameStyle.Bold(true)
	}

	first := cursor + markerStyle.Render(marker) + "  " + nameStyle.Render(it.Name)
	if tag != "" {
		first += "  " + kit.BadgeStyle().Render(tag)
	}

	descStyle := kit.DimStyle()
	desc := strings.TrimSpace(it.Description)
	if desc == "" {
		desc = "(no description)"
	}
	desc = kit.TruncateText(desc, panel.ContentWidth()-12)
	second := strings.Repeat(" ", 6) + descStyle.Render(desc)

	return first + "\n" + second
}

func (s *IdentitySelector) renderHints() string {
	keys := kit.HintLine(
		"↑/↓ navigate",
		"Enter activate",
		"Shift+N new",
		"Shift+E edit",
		"Esc close",
	)
	if s.hint == "" {
		return keys
	}
	hintStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Warning)
	return keys + "\n  " + hintStyle.Render(s.hint)
}
