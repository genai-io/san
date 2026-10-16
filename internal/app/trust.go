package app

import (
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/mcp"
	"github.com/genai-io/san/internal/plugin"
	"github.com/genai-io/san/internal/setting"
)

// workspaceTrustFindings lists what cwd's own configuration would turn on
// once trusted: hooks, allow rules, MCP servers, plugins.
func workspaceTrustFindings(cwd string) []string {
	findings := setting.ProjectSettingsFindings(cwd)
	findings = append(findings, mcp.ProjectServerFindings(cwd)...)
	return append(findings, plugin.ProjectPluginFindings(cwd)...)
}

// resolveWorkspaceTrust asks before cwd's configuration applies. A folder
// with nothing to review is trusted for this run without asking. It reports
// whether the user quit instead of answering.
func resolveWorkspaceTrust(cwd string) (quit bool, err error) {
	if setting.IsWorkspaceTrusted(cwd) {
		return false, nil
	}
	findings := workspaceTrustFindings(cwd)
	if len(findings) == 0 {
		setting.TrustWorkspaceForSession(cwd)
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

// warnUntrustedWorkspace is the headless form: no prompt, so an untrusted
// folder's configuration stays off and stderr says what was skipped.
func warnUntrustedWorkspace(cwd string) {
	if setting.IsWorkspaceTrusted(cwd) {
		return
	}
	findings := workspaceTrustFindings(cwd)
	if len(findings) == 0 {
		setting.TrustWorkspaceForSession(cwd)
		return
	}
	fmt.Fprintf(os.Stderr, "san: %s is not trusted, so its configuration was not applied:\n  - %s\n"+
		"Run san there once and trust it, or set SAN_TRUST_WORKSPACE=1 for this run.\n",
		setting.TrustKey(cwd), strings.Join(findings, "\n  - "))
}

// noteUntrustedWorkspace covers a mid-session move into another folder: its
// configuration stays off, and the chat says so rather than prompting.
func (m *model) noteUntrustedWorkspace(cwd string) {
	if setting.IsWorkspaceTrusted(cwd) {
		return
	}
	findings := workspaceTrustFindings(cwd)
	if len(findings) == 0 {
		setting.TrustWorkspaceForSession(cwd)
		return
	}
	m.conv.AddNotice(fmt.Sprintf("%s is not trusted, so its configuration is off:\n- %s\nStart San there to review and trust it.",
		setting.TrustKey(cwd), strings.Join(findings, "\n- ")))
}

type trustPrompt struct {
	folder   string
	findings []string
	cursor   int // 0 trust, 1 continue without
	trusted  bool
	quit     bool
}

func (p trustPrompt) Init() tea.Cmd { return nil }

func (p trustPrompt) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
	dim := lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim)
	warn := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Warning)
	active := lipgloss.NewStyle().Bold(true).Foreground(kit.CurrentTheme.Focus)

	var b strings.Builder
	b.WriteString(title.Render("Do you trust the files in this folder?") + "\n\n")
	b.WriteString("  " + p.folder + "\n\n")
	b.WriteString(dim.Render("  Its configuration would let San:") + "\n")
	for _, f := range p.findings {
		b.WriteString(warn.Render("    • ") + f + "\n")
	}
	b.WriteString("\n" + dim.Render("  Trust covers every worktree of this repository. Only trust code you know.") + "\n\n")
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
