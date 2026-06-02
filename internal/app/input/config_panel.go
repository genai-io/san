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
	innerWidth := boxWidth - 4 // Padding(1, 2)

	rule := configRuleStyle.Render(strings.Repeat("─", innerWidth))

	var b strings.Builder
	b.WriteString(c.renderHeader())
	b.WriteString("\n")
	b.WriteString(rule)
	b.WriteString("\n\n")
	b.WriteString(p.Render(innerWidth))
	b.WriteString("\n")
	b.WriteString(rule)
	b.WriteString("\n")
	b.WriteString(c.renderHint(p.HintLine()))

	box := lipgloss.NewStyle().
		Width(boxWidth).
		Height(boxHeight).
		Padding(1, 2).
		Render(b.String())
	return lipgloss.Place(c.width, c.height-2, lipgloss.Center, lipgloss.Top, box)
}

// boxSize caps the popup dimensions: width holds the form within a
// readable column (~90 chars of content), height fits the typical content
// without stretching to the full terminal.
func (c *ConfigSelector) boxSize() (w, h int) {
	w = max(60, c.width-6)
	w = min(w, 92)
	h = max(20, c.height-4)
	h = min(h, 34)
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

// renderHeader returns the breadcrumb + tab pills. When only one panel is
// registered, the breadcrumb already shows its title so we skip the pills.
func (c *ConfigSelector) renderHeader() string {
	bc := configBreadcrumbDimStyle.Render("/config")
	if len(c.panels) == 1 {
		return bc + configBreadcrumbDimStyle.Render(" › ") +
			configBreadcrumbStyle.Render(c.panels[0].Title())
	}
	tabs := make([]kit.PanelTab, len(c.panels))
	for i, p := range c.panels {
		tabs[i] = kit.PanelTab{Name: p.Title(), Show: true}
	}
	return bc + "\n\n" + kit.RenderPanelTabs(tabs, c.index)
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
)
