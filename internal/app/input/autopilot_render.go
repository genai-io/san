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
				keycap("↑↓")+" navigate", keycap("enter")+" edit/toggle", keycap("←→")+" adjust",
				keycap("esc")+" save & close",
			),
		)
	}
}

func (p *AutopilotSelector) frame(header, body, hint string) string {
	return panelFrame(p.width, p.height, p.innerWidth(), header, body, hint)
}

// panelFrame stacks header + a faint hairline + body + hint into a column of
// exactly w cells, wraps it in a rounded card, and centers it. The card adds
// border+padding around the column without re-setting a width, so nothing
// re-wraps.
func panelFrame(width, height, w int, header, body, hint string) string {
	rule := apFaintRuleStyle.Render(strings.Repeat("─", w))
	col := lipgloss.NewStyle().Width(w).Render(header + "\n" + rule + "\n\n" + body + "\n\n" + hint)
	return lipgloss.Place(width, height-2, lipgloss.Center, lipgloss.Center, apCardStyle.Render(col))
}

// header renders the title lockup ("✦ Autopilot") with a sub-view crumb when
// inside an editor.
func (p *AutopilotSelector) header(sub string) string {
	left := apTitleGlyphStyle.Render("✦ ") + apTitleStyle.Render("Autopilot")
	if sub != "" {
		left += apBreadcrumbDimStyle.Render("  ›  ") + apBreadcrumbSubStyle.Render(sub)
	}
	return left
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
			if row.desc != "" {
				b.WriteString(apSummaryStyle.Render(" · " + row.desc))
			}
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
	labelStyle, descStyle := apLabelStyle, apDescStyle
	if row.idle {
		labelStyle, descStyle = apSummaryStyle, apSummaryStyle
	}
	// The blank where a toggle draws its "[✓] " keeps every label in one column.
	label := p.cursorMark(i) + "    " + labelStyle.Render(apPad(row.label, apLabelWidth))
	right := apSummaryStyle.Render(row.value)
	room := width - lipgloss.Width(label) - lipgloss.Width(right) - 4
	desc := ""
	if row.desc != "" && room > 0 {
		desc = "  " + descStyle.Render(kit.TruncateText(row.desc, room))
	}
	left := label + desc
	gap := max(width-lipgloss.Width(left)-lipgloss.Width(right), 1)
	return left + strings.Repeat(" ", gap) + right
}

// renderToggle draws "▸ [✓] Suggest   your next input · Tab to accept". A
// disabled or idle row is drawn dim end to end; an idle one keeps its check.
func (p *AutopilotSelector) renderToggle(i int, row apRow) string {
	mark, labelStyle, descStyle := "[ ]", apLabelStyle, apDescStyle
	if row.on && !row.disabled {
		mark = "[✓]"
	}
	switch {
	case row.disabled || row.idle:
		mark, labelStyle, descStyle = apSummaryStyle.Render(mark), apSummaryStyle, apSummaryStyle
	case row.on:
		mark = apCheckStyle.Render(mark)
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
)
