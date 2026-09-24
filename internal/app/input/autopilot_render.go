package input

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/kit"
)

// Render implements overlayPanel: it frames whichever sub-view is active in a
// centered box matching the /settings overlay.
func (p *AutopilotSelector) Render() string {
	if !p.active {
		return ""
	}
	switch p.view {
	case apSystemPrompt:
		return p.frame(
			p.header("System prompt"),
			apDescStyle.Render("Safety rules are fixed and always apply; this edits how it drives. Applies to this session only.")+
				"\n\n"+p.prompt.View(),
			kit.HintLine(keycap("esc")+" back"),
		)
	case apMission:
		return p.frame(p.header("Mission"), p.renderMission(), p.missionHint())
	case apExport:
		return p.frame(p.header("Save preset"), p.renderExport(), p.exportHint())
	case apImport:
		return p.frame(p.header("Load preset"), p.renderImport(), p.importHint())
	case apModel:
		return p.frame(p.header(p.modelCrumb()), p.renderModel(), p.modelHint())
	default:
		return p.frame(
			p.header(""),
			p.renderMenu(p.innerWidth()),
			kit.HintLine(
				keycap("↑↓")+" navigate", keycap("space")+" edit/toggle", keycap("←→")+" adjust",
				keycap("enter")+" save", keycap("esc")+" discard",
			),
		)
	}
}

// frame stacks header + a faint hairline + body + hint into a fixed-width column,
// wraps it in a rounded card, and centers it. The column is built at exactly
// innerWidth and the card adds border+padding around it without re-setting a
// width, so nothing re-wraps.
func (p *AutopilotSelector) frame(header, body, hint string) string {
	w := p.innerWidth()
	rule := apFaintRuleStyle.Render(strings.Repeat("─", w))

	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n")
	b.WriteString(rule)
	b.WriteString("\n\n")
	b.WriteString(body)
	b.WriteString("\n\n")
	b.WriteString(hint)

	col := lipgloss.NewStyle().Width(w).Render(b.String())
	card := apCardStyle.Render(col)
	// Center on both axes so the card sits balanced in the terminal.
	return lipgloss.Place(p.width, p.height-2, lipgloss.Center, lipgloss.Center, card)
}

// header renders the title lockup ("✦ Autopilot") with a sub-view crumb when
// inside an editor, and an "● unsaved" tag pinned right.
func (p *AutopilotSelector) header(sub string) string {
	left := apTitleGlyphStyle.Render("✦ ") + apTitleStyle.Render("Autopilot")
	if sub != "" {
		left += apBreadcrumbDimStyle.Render("  ›  ") + apBreadcrumbSubStyle.Render(sub)
	}
	if !p.Dirty() {
		return left
	}
	right := apUnsavedDotStyle.Render("●") + " " + apUnsavedTextStyle.Render("unsaved")
	gap := max(p.innerWidth()-lipgloss.Width(left)-lipgloss.Width(right), 1)
	return left + strings.Repeat(" ", gap) + right
}

// ── Menu ────────────────────────────────────────────────────────────────

// apLabelWidth aligns the descriptions: every label is padded to the widest.
const apLabelWidth = 8

func (p *AutopilotSelector) renderMenu(width int) string {
	var b strings.Builder
	for i, row := range p.rows() {
		switch row.kind {
		case apRowSection:
			b.WriteString(apSectionStyle.Render(strings.ToUpper(row.label)))
		case apRowEntry:
			b.WriteString(p.renderEntry(i, row, width))
		case apRowToggle:
			b.WriteString(p.renderToggle(i, row))
		case apRowSpacer:
			// blank line
		}
		b.WriteString("\n")
	}
	if p.status != "" {
		b.WriteString("\n")
		b.WriteString(apSummaryStyle.Render(p.status))
	}
	return b.String()
}

func (p *AutopilotSelector) cursorMark(i int) string {
	if i == p.cursor {
		return apCursorStyle.Render("▸ ")
	}
	return "  "
}

// renderEntry draws "▸ Mission   the goal it works toward …   ship it · ready":
// label, muted description, and the current value right-aligned. The
// description is truncated first on a narrow card so the value always shows.
func (p *AutopilotSelector) renderEntry(i int, row apRow, width int) string {
	label := p.cursorMark(i) + apLabelStyle.Render(apPad(row.label, apLabelWidth))
	right := apSummaryStyle.Render(row.value)
	room := width - lipgloss.Width(label) - lipgloss.Width(right) - 4
	desc := ""
	if row.desc != "" && room > 0 {
		desc = "  " + apDescStyle.Render(kit.TruncateText(row.desc, room))
	}
	left := label + desc
	gap := max(width-lipgloss.Width(left)-lipgloss.Width(right), 1)
	return left + strings.Repeat(" ", gap) + right
}

// renderToggle draws "▸ [✓] Suggest   your next input · Tab to accept". A
// disabled row is drawn dim end to end.
func (p *AutopilotSelector) renderToggle(i int, row apRow) string {
	mark, labelStyle, descStyle := "[ ]", apLabelStyle, apDescStyle
	switch {
	case row.disabled:
		mark, labelStyle, descStyle = apSummaryStyle.Render(mark), apSummaryStyle, apSummaryStyle
	case row.on:
		mark = apCheckStyle.Render("[✓]")
	}
	return p.cursorMark(i) + mark + " " + labelStyle.Render(apPad(row.label, apLabelWidth)) + "  " + descStyle.Render(row.desc)
}

// apPad pads a label to w cells; a longer one is left whole.
func apPad(s string, w int) string {
	return s + strings.Repeat(" ", max(w-lipgloss.Width(s), 0))
}

// ── Styles ──────────────────────────────────────────────────────────────

var (
	// Title lockup: teal star + accent-bold wordmark + a muted tagline.
	apTitleGlyphStyle    = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Focus)
	apTitleStyle         = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Accent).Bold(true)
	apBreadcrumbDimStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim)
	apBreadcrumbSubStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Text).Bold(true)

	// Rounded card that frames the whole panel.
	apCardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(kit.CurrentTheme.Border).
			Padding(1, 2)

	// apFaintRuleStyle is the barely-there hairline used for the header
	// separator and the section dividers so they don't overshadow content.
	apFaintRuleStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim).Faint(true)
	// Section labels sit quietly (muted, not accent-bold) so "STEER" / "MISSION"
	// read as soft signposts, not headings competing with the copilot breadcrumb.
	apSectionStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted).Bold(true)
	apLabelStyle   = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Text)
	apDescStyle    = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	apSummaryStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim)
	apCursorStyle  = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Accent).Bold(true)
	apCheckStyle   = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Success)
	apValueStyle   = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Accent).Underline(true)

	apUnsavedDotStyle  = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Warning).Bold(true)
	apUnsavedTextStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Warning)
)
