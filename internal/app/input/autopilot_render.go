package input

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/kit"
)

// Render implements overlayPanel: it frames whichever sub-view is active in a
// centered box matching the /config overlay.
func (p *AutopilotSelector) Render() string {
	if !p.active {
		return ""
	}
	switch p.view {
	case apSteeringPrompt:
		return p.frame(
			p.header("Steering Prompt"),
			p.prompt.View(),
			kit.HintLine(keycap("esc")+" back", "edits apply on Save"),
		)
	case apMission:
		return p.frame(
			p.header("Mission"),
			p.renderMission(),
			p.missionHint(),
		)
	case apExport:
		return p.frame(p.header("Export"), p.renderExport(), p.exportHint())
	case apImport:
		return p.frame(p.header("Import"), p.renderImport(), p.importHint())
	default:
		return p.frame(
			p.header(""),
			p.renderMenu(p.innerWidth()),
			kit.HintLine(
				keycap("↑↓")+" navigate", keycap("space")+" toggle", keycap("enter")+" select",
				keycap("e")+" export", keycap("i")+" import", keycap("esc")+" close",
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

// header renders the title lockup ("✦ Autopilot" + tagline) with a sub-view
// crumb when inside an editor, and an "● unsaved" tag pinned right.
func (p *AutopilotSelector) header(sub string) string {
	left := apTitleGlyphStyle.Render("✦ ") + apTitleStyle.Render("Autopilot")
	if sub != "" {
		left += apBreadcrumbDimStyle.Render("  ›  ") + apBreadcrumbSubStyle.Render(sub)
	} else {
		left += apTaglineStyle.Render("   your session's co-pilot")
	}
	if !p.Dirty() {
		return left
	}
	right := apUnsavedDotStyle.Render("●") + " " + apUnsavedTextStyle.Render("unsaved")
	gap := max(p.innerWidth()-lipgloss.Width(left)-lipgloss.Width(right), 1)
	return left + strings.Repeat(" ", gap) + right
}

// ── Menu ────────────────────────────────────────────────────────────────

func (p *AutopilotSelector) renderMenu(width int) string {
	var b strings.Builder
	for i, row := range p.rows() {
		switch row.kind {
		case apRowSection:
			b.WriteString(p.renderSection(row.label, width))
		case apRowEntry:
			b.WriteString(p.renderEntry(i, row, width))
		case apRowSteer:
			b.WriteString(p.renderSteer(i, row))
		case apRowInt:
			b.WriteString(p.renderInt(i, row))
		case apRowSaveStart:
			b.WriteString(p.renderSaveStart(i))
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

func (p *AutopilotSelector) renderSection(label string, width int) string {
	up := strings.ToUpper(label)
	ruleLen := max(width-lipgloss.Width(up)-1, 1)
	return apSectionStyle.Render(up) + " " + apFaintRuleStyle.Render(strings.Repeat("─", ruleLen))
}

// renderEntry draws an editor entry: "▸ Steering Prompt  how it drives … built-in".
// The value hint sits right-aligned; enter-to-open is covered by the bottom hint.
func (p *AutopilotSelector) renderEntry(i int, row apRow, width int) string {
	left := p.cursorMark(i) + apLabelStyle.Render(row.label)
	if row.desc != "" {
		left += "  " + apDescStyle.Render(row.desc)
	}
	right := ""
	if row.summary != nil {
		right = apSummaryStyle.Render(row.summary(p.snap))
	}
	gap := max(width-lipgloss.Width(left)-lipgloss.Width(right), 1)
	return left + strings.Repeat(" ", gap) + right
}

func (p *AutopilotSelector) renderSteer(i int, row apRow) string {
	mark := "[ ]"
	if row.get(p.snap) {
		mark = apCheckStyle.Render("[✓]")
	}
	line := p.cursorMark(i) + mark + " " + apLabelStyle.Render(row.label)
	if row.desc != "" {
		line += "  " + apDescStyle.Render(row.desc)
	}
	return line
}

func (p *AutopilotSelector) renderInt(i int, row apRow) string {
	value, suffix := strconv.Itoa(p.snap.ResolvedMaxContinuations()), "times"
	if p.snap.ContinuationsUnlimited() {
		value, suffix = "∞", "until the mission is done"
	}
	if p.editing && i == p.cursor {
		// Teach the affordance where it's needed: 0 is meaningless as a cap, so it
		// reads as "don't cap me".
		value, suffix = p.editingBuffer+"_", "times · 0 = no limit"
	}
	indent := strings.Repeat("  ", row.indent+1)
	chip := apChipStyle.Render("(") + apValueStyle.Render(value) + apChipStyle.Render(")")
	return indent + p.cursorMark(i) + row.label + " " + chip + " " + apDescStyle.Render(suffix)
}

// renderSaveStart draws Save and Start as two filled pill keys side by side —
// both sit on the neutral keycap surface so they read as buttons at rest. The
// focused pick (←/→) lights its label bold green; the other stays neutral. Focus
// is carried by that color, so this row skips the ▸ mark and indents to sit under
// the content column instead.
func (p *AutopilotSelector) renderSaveStart(i int) string {
	seg := func(active bool, label string) string {
		if i == p.cursor && active {
			return apButtonFocusedStyle.Render(label)
		}
		return apButtonIdleStyle.Render(label)
	}
	save := seg(p.saveCursor == 0, "Save")
	start := seg(p.saveCursor == 1, "Start")
	return "  " + save + "  " + start
}

// ── Styles ──────────────────────────────────────────────────────────────

var (
	// Title lockup: teal star + accent-bold wordmark + a muted tagline.
	apTitleGlyphStyle    = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Focus)
	apTitleStyle         = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Accent).Bold(true)
	apTaglineStyle       = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted).Italic(true)
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
	apChipStyle    = lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim)

	apUnsavedDotStyle  = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Warning).Bold(true)
	apUnsavedTextStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Warning)

	// Save/Start buttons: filled neutral pills on the panel's keycap surface, so
	// they read as raised keys at rest — depth from the fill, no frame. Focus is a
	// light touch: the picked pill keeps the same surface and just takes a bold
	// green label; no heavy fill swap.
	apButtonBaseStyle    = lipgloss.NewStyle().Padding(0, 2).Background(kit.SearchBg)
	apButtonIdleStyle    = apButtonBaseStyle.Foreground(kit.CurrentTheme.Text)
	apButtonFocusedStyle = apButtonBaseStyle.Foreground(kit.CurrentTheme.Success).Bold(true)
)
