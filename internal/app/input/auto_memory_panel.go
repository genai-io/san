package input

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/setting"
)

var memoryScopes = [...]setting.Scope{setting.ScopeUser, setting.ScopeProject}

type autoMemoryPanel struct {
	workspace func() (string, *setting.Settings)
	cursor    int
	values    [len(memoryScopes)]*bool
	status    string
	err       error
}

func (p *autoMemoryPanel) Title() string { return "Auto Memory" }
func (p *autoMemoryPanel) Dirty() bool   { return false }
func (p *autoMemoryPanel) HintLine() string {
	return keycap("↑↓") + " navigate  " + keycap("enter") + " toggle & save"
}

func (p *autoMemoryPanel) Enter() {
	p.cursor, p.err = 0, nil
	p.status = "settings unavailable"
	if p.workspace != nil {
		_, svc := p.workspace()
		p.load(svc)
	}
}

func (p *autoMemoryPanel) load(svc *setting.Settings) {
	if svc == nil {
		p.values = [len(memoryScopes)]*bool{}
		p.status = "settings unavailable"
		return
	}
	for i, scope := range memoryScopes {
		p.values[i] = setting.AutoMemoryAt(scope)
	}
	p.status = "effective: " + onOff(svc.AutoMemory()) + " (" + memorySource(p.values[0], p.values[1]) + ")"
}

func (p *autoMemoryPanel) HandleKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "up", "k":
		p.cursor = max(0, p.cursor-1)
	case "down", "j":
		p.cursor = min(len(memoryScopes)-1, p.cursor+1)
	case "enter":
		p.toggle()
	}
	return nil, false
}

func (p *autoMemoryPanel) toggle() {
	if p.workspace == nil {
		return
	}
	cwd, svc := p.workspace()
	p.load(svc)
	if svc == nil {
		return
	}
	// Flip this scope's own or inherited value, not the effective override.
	value := p.values[p.cursor]
	if value == nil {
		value = p.values[0]
	}
	p.err = setAutoMemory(svc, cwd, value != nil && !*value, memoryScopes[p.cursor])
	p.load(svc)
}

func (p *autoMemoryPanel) Render(width, _ int) string {
	muted := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	focus := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Focus).Bold(true)
	var b strings.Builder
	b.WriteString(muted.Render(p.status) + "\n\n")
	for i, label := range [...]string{"User", "Project"} {
		cursor, status := "  ", "inherit"
		if i == p.cursor {
			cursor = focus.Render("▸ ")
		}
		if p.values[i] != nil {
			status = onOff(*p.values[i])
		}
		gap := max(1, width-2-lipgloss.Width(label)-lipgloss.Width(status))
		b.WriteString(cursor + label + strings.Repeat(" ", gap) + muted.Render(status) + "\n")
	}
	b.WriteString("\n" + muted.Render("Browse files and stores with /memory."))
	if p.err != nil {
		b.WriteString("\n" + lipgloss.NewStyle().Foreground(kit.CurrentTheme.Error).Width(width).Render(p.err.Error()))
	}
	return b.String()
}
