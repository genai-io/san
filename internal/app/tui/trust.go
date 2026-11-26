package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/app/tui/widgets"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/setting/trust"
)

// resolveWorkspaceTrust asks before cwd's configuration applies. It reports
// whether the user quit instead of answering.
func resolveWorkspaceTrust(cwd string) (quit bool, err error) {
	findings := trust.ReviewAndTrustIfClean(cwd)
	if len(findings) == 0 {
		return false, nil
	}
	final, err := tea.NewProgram(trustPrompt{folder: setting.TrustKey(cwd), findings: findings}).Run()
	if err != nil {
		return false, fmt.Errorf("workspace trust prompt: %w", err)
	}
	switch p := final.(trustPrompt); {
	case p.quit:
		return true, nil
	case p.trusted:
		return false, setting.TrustWorkspace(cwd)
	}
	return false, nil
}

// noteUntrustedWorkspace covers a mid-session move into another folder: its
// configuration stays off, and the chat says so rather than prompting.
func (m *model) noteUntrustedWorkspace(cwd string) {
	findings := trust.ReviewAndTrustIfClean(cwd)
	if len(findings) == 0 {
		return
	}
	m.conv.AddNotice(fmt.Sprintf("%s is not trusted, so its configuration is off:\n- %s\nRun /trust to turn it on.",
		setting.TrustKey(cwd), strings.Join(findings, "\n- ")))
}

// trustWorkspace is /trust: trust the working directory's folder and apply
// its configuration now, without restarting.
func (m *model) trustWorkspace() string {
	cwd := m.env.CWD
	key := setting.TrustKey(cwd)
	if setting.IsWorkspaceTrusted(cwd) {
		return key + " is already trusted."
	}
	findings := trust.Findings(cwd)
	if err := setting.TrustWorkspace(cwd); err != nil {
		return "Failed to trust " + key + ": " + err.Error()
	}
	m.ReloadProjectContext(cwd)
	if len(findings) == 0 {
		return "Trusted " + key + "."
	}
	return fmt.Sprintf("Trusted %s. Now on:\n- %s", key, strings.Join(findings, "\n- "))
}

type trustPrompt struct {
	folder   string
	findings []string
	cursor   int // 0 trust, 1 continue without
	width    int
	trusted  bool
	quit     bool
}

func (p trustPrompt) Init() tea.Cmd { return nil }

func (p trustPrompt) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		p.width = size.Width
	}
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return p, nil
	}
	switch key.String() {
	case "up", "k", "down", "j", "tab":
		p.cursor = 1 - p.cursor
	case "y":
		p.trusted = true
		return p, tea.Quit
	case "n":
		return p, tea.Quit
	case "enter":
		p.trusted = p.cursor == 0
		return p, tea.Quit
	case "esc", "ctrl+c", "q":
		p.quit = true
		return p, tea.Quit
	}
	return p, nil
}

func (p trustPrompt) View() tea.View {
	title := lipgloss.NewStyle().Bold(true)
	dim := lipgloss.NewStyle().Foreground(widgets.CurrentTheme.TextDim)
	warn := lipgloss.NewStyle().Foreground(widgets.CurrentTheme.Warning)
	active := lipgloss.NewStyle().Bold(true).Foreground(widgets.CurrentTheme.Focus)

	// wrap fits s after indent, continuing lines under it; long paths break.
	wrap := func(indent, s string) string {
		if p.width <= len(indent)+10 {
			return s
		}
		lines := strings.Split(ansi.Wrap(s, p.width-len(indent), " /"), "\n")
		return strings.Join(lines, "\n"+strings.Repeat(" ", len(indent)))
	}

	var b strings.Builder
	b.WriteString(title.Render("Do you trust the files in this folder?") + "\n\n")
	b.WriteString("  " + wrap("  ", p.folder) + "\n\n")
	b.WriteString(dim.Render("  Its configuration would let San:") + "\n")
	for _, f := range p.findings {
		b.WriteString(warn.Render("    • ") + wrap("      ", f) + "\n")
	}
	b.WriteString("\n  " + dim.Render(wrap("  ", "Trust covers every worktree of this repository. Only trust code you know.")) + "\n\n")
	for i, label := range []string{"Yes, trust this folder", "No, continue without its configuration"} {
		if i == p.cursor {
			b.WriteString(active.Render("  ▸ "+label) + "\n")
		} else {
			b.WriteString("    " + label + "\n")
		}
	}
	b.WriteString("\n" + dim.Render("  ↑/↓ move · enter confirm · esc quit") + "\n")
	return tea.NewView(b.String())
}
