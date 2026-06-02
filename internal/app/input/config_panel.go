// /config popup shell. ConfigSelector frames one or more sub-panels with
// a "/config" breadcrumb and a tab strip when multiple panels are
// registered, an esc/cancel handler, and centered full-width placement
// that matches the /plugin and /model overlays. Each sub-panel implements
// Panel and owns its own body + hint line. To add a new panel: implement
// Panel and append it to the panels slice in NewConfigSelector.
package input

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/genai-io/gen-code/internal/app/kit"
	"github.com/genai-io/gen-code/internal/setting"
)

// Panel is one /config sub-panel.
//
// Lifecycle:
//   - Enter(): reset working state on (re)activation.
//   - HandleKey(): handle one keypress; (cmd, done) — done=true asks the
//     shell to dismiss the popup (e.g. after Save).
//   - Render(width): the panel body, framed by the shell.
//   - HintLine(): the muted bottom hint (e.g. "↑↓ navigate · …"). Shown
//     under the body; the shell appends its own "· esc cancel".
type Panel interface {
	Title() string
	Enter()
	HandleKey(msg tea.KeyMsg) (tea.Cmd, bool)
	Render(width int) string
	HintLine() string
	// Dirty reports whether the panel has unsaved edits. The shell uses
	// this to pin a "● unsaved" tag to the top-right of the header.
	Dirty() bool
}

// ConfigSavedMsg is emitted on a successful Save so the app can show a
// transient confirmation.
type ConfigSavedMsg struct{ Scope string }

// ConfigSelector is the /config popup. Today only Self-Learning is
// registered; adding a sibling panel (Provider, Permissions, Appearance,
// …) is a one-liner in NewConfigSelector.
type ConfigSelector struct {
	panels []Panel
	index  int

	active bool
	width  int
	height int
}

// NewConfigSelector wires the popup with its set of panels. Order is
// preserved by the tab strip.
func NewConfigSelector(settings *setting.Settings) ConfigSelector {
	return ConfigSelector{
		panels: []Panel{newSelfLearnPanel(settings)},
	}
}

// Enter activates the popup with the first panel focused.
func (c *ConfigSelector) Enter(width, height int) {
	c.width = width
	c.height = height
	c.active = true
	if c.index >= len(c.panels) {
		c.index = 0
	}
	if p := c.activePanel(); p != nil {
		p.Enter()
	}
}

// IsActive implements the popup interface.
func (c *ConfigSelector) IsActive() bool { return c.active }

// HandleKeypress implements the popup interface. Esc dismisses the popup;
// Ctrl-Tab cycles panels (when more than one is registered); everything
// else delegates to the active panel.
func (c *ConfigSelector) HandleKeypress(msg tea.KeyMsg) tea.Cmd {
	if !c.active {
		return nil
	}
	switch msg.String() {
	case "esc":
		c.active = false
		return nil
	case "ctrl+tab":
		if len(c.panels) > 1 {
			c.index = (c.index + 1) % len(c.panels)
			c.panels[c.index].Enter()
		}
		return nil
	}
	p := c.activePanel()
	if p == nil {
		return nil
	}
	cmd, done := p.HandleKey(msg)
	if done {
		c.active = false
	}
	return cmd
}

// Render frames the active panel with breadcrumb + tab strip on top, the
// panel body, and the combined hint line at the bottom. Centered on screen
// via lipgloss.Place, matching /plugin — but with a capped width so form
// rows don't sprawl across an ultra-wide terminal (values would otherwise
// drift far from their labels).
func (c *ConfigSelector) Render() string {
	if !c.active || len(c.panels) == 0 {
		return ""
	}
	p := c.activePanel()
	boxWidth, boxHeight := c.boxSize()
	gutter := 2                        // horizontal gutter on each side of body content
	innerWidth := boxWidth - 2*gutter  // width body rows render to
	pad := strings.Repeat(" ", gutter) // gutter prefix for non-rule lines

	// Top/bottom rules extend the full box width so they read as section
	// chrome, not as content indented into the gutter.
	rule := configRuleStyle.Render(strings.Repeat("─", boxWidth))

	// indent indents every line of s by the gutter except blank lines
	// (which stay blank so they don't carry trailing whitespace).
	indent := func(s string) string {
		lines := strings.Split(s, "\n")
		for i, ln := range lines {
			if ln == "" {
				continue
			}
			lines[i] = pad + ln
		}
		return strings.Join(lines, "\n")
	}

	var b strings.Builder
	b.WriteString(indent(c.renderHeader(innerWidth)))
	b.WriteString("\n")
	b.WriteString(rule)
	b.WriteString("\n\n")
	b.WriteString(indent(p.Render(innerWidth)))
	b.WriteString("\n")
	b.WriteString(rule)
	b.WriteString("\n")
	b.WriteString(indent(c.renderHint(p.HintLine())))

	box := lipgloss.NewStyle().
		Width(boxWidth).
		Height(boxHeight).
		Padding(1, 0). // vertical padding only — gutter is in-content
		Render(b.String())
	// Center the popup so it sits balanced on a wide terminal instead of
	// hugging the left edge.
	return lipgloss.Place(c.width, c.height-2, lipgloss.Center, lipgloss.Top, box)
}

// boxSize sizes the popup the same way /plugin does: width fills the
// terminal minus a 6-col margin, height fills it minus a 4-row margin.
// No upper cap — the popup stretches with the terminal so the form
// reads as a real overlay, not a narrow card.
func (c *ConfigSelector) boxSize() (w, h int) {
	w = max(60, c.width-6)
	h = max(18, c.height-4)
	return w, h
}

// ActivePanel returns the currently focused panel; nil when none are
// registered. Exported for tests.
func (c *ConfigSelector) ActivePanel() Panel { return c.activePanel() }

func (c *ConfigSelector) activePanel() Panel {
	if len(c.panels) == 0 {
		return nil
	}
	return c.panels[c.index]
}

// renderHeader returns the breadcrumb (or tab pills, when multiple
// panels are registered) with an optional "● unsaved" tag pinned to
// the right edge of the header line.
func (c *ConfigSelector) renderHeader(width int) string {
	var left string
	if len(c.panels) == 1 {
		left = configBreadcrumbDimStyle.Render("/config") +
			configBreadcrumbDimStyle.Render(" › ") +
			configBreadcrumbStyle.Render(c.panels[0].Title())
	} else {
		tabs := make([]kit.PanelTab, len(c.panels))
		for i, p := range c.panels {
			tabs[i] = kit.PanelTab{Name: p.Title(), Show: true}
		}
		return configBreadcrumbDimStyle.Render("/config") + "\n\n" +
			kit.RenderPanelTabs(tabs, c.index)
	}

	right := ""
	if c.activePanel() != nil && c.activePanel().Dirty() {
		right = configUnsavedDotStyle.Render("●") + " " +
			configUnsavedTextStyle.Render("unsaved")
	}
	if right == "" {
		return left
	}
	gap := max(width-lipgloss.Width(left)-lipgloss.Width(right), 1)
	return left + strings.Repeat(" ", gap) + right
}

func (c *ConfigSelector) renderHint(panelHint string) string {
	parts := []string{}
	if panelHint != "" {
		parts = append(parts, panelHint)
	}
	parts = append(parts, "esc cancel")
	if len(c.panels) > 1 {
		parts = append(parts, "ctrl-tab switch panel")
	}
	return kit.HintLine(parts...)
}

var (
	configBreadcrumbDimStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim)
	configBreadcrumbStyle    = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Accent).Bold(true)
	configRuleStyle          = lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim)
	configUnsavedDotStyle    = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Warning).Bold(true)
	configUnsavedTextStyle   = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Warning)
)
