package input

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/setting"
)

var memoryScopes = [...]struct {
	scope setting.Scope
	label string
}{
	{setting.ScopeUser, "User"},
	{setting.ScopeProject, "Project"},
}

const (
	memoryEnabled = iota
	memoryLookup
	memoryTimeout
	memoryRowCount
)

type autoMemoryPanel struct {
	workspace func() (string, *setting.Settings)
	cursor    int
	values    [len(memoryScopes)]*bool
	modes     [len(memoryScopes)]setting.MemoryRecallMode
	timeouts  [len(memoryScopes)]string
	status    string
	err       error
	editing   bool
	input     textinput.Model
}

func (p *autoMemoryPanel) Title() string { return "Auto Memory" }
func (p *autoMemoryPanel) Dirty() bool   { return false }
func (p *autoMemoryPanel) Modal() bool   { return p.editing }
func (p *autoMemoryPanel) HintLine() string {
	if p.editing {
		return keycap("enter") + " save  " + keycap("esc") + " cancel"
	}
	action := [...]string{"toggle", "cycle", "edit"}[p.cursor%memoryRowCount]
	return keycap("↑↓") + " navigate  " + keycap("enter") + " " + action
}

func (p *autoMemoryPanel) Enter() {
	p.cursor, p.err, p.editing = 0, nil, false
	p.status = "settings unavailable"
	if p.workspace != nil {
		cwd, svc := p.workspace()
		p.load(cwd, svc)
	}
}

func (p *autoMemoryPanel) load(cwd string, svc *setting.Settings) {
	if svc == nil {
		p.values = [len(memoryScopes)]*bool{}
		p.modes = [len(memoryScopes)]setting.MemoryRecallMode{}
		p.timeouts = [len(memoryScopes)]string{}
		p.status = "settings unavailable"
		return
	}
	for i, row := range memoryScopes {
		p.values[i] = setting.AutoMemoryAt(row.scope)
		p.modes[i], p.timeouts[i] = setting.MemoryRecallAt(cwd, row.scope)
	}
	cfg := svc.MemoryRecall()
	lookup := recallModeLabel(cfg.Mode)
	if cfg.Mode == setting.MemoryRecallWait {
		lookup += " · " + cfg.Timeout.String()
	}
	p.status = "effective: " + onOff(svc.AutoMemory()) + " (" + memorySource(p.values[0], p.values[1]) + ") · " + lookup
}

func (p *autoMemoryPanel) HandleKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	if p.editing {
		return p.handleTimeoutKey(msg), false
	}
	switch msg.String() {
	case "up", "k":
		p.cursor = max(0, p.cursor-1)
	case "down", "j":
		p.cursor = min(len(memoryScopes)*memoryRowCount-1, p.cursor+1)
	case "enter":
		p.activate()
	}
	return nil, false
}

func (p *autoMemoryPanel) activate() {
	if p.workspace == nil {
		return
	}
	cwd, svc := p.workspace()
	p.load(cwd, svc)
	if svc == nil {
		return
	}
	i := p.cursor / memoryRowCount
	scope := memoryScopes[i].scope
	p.err = nil
	switch p.cursor % memoryRowCount {
	case memoryEnabled:
		// Flip the scoped or inherited value, not the effective override.
		value := p.values[i]
		if value == nil {
			value = p.values[0]
		}
		p.err = setAutoMemory(svc, cwd, value != nil && !*value, scope)
	case memoryLookup:
		var next setting.MemoryRecallMode
		switch p.modes[i] {
		case setting.MemoryRecallFast:
			next = setting.MemoryRecallWait
		case setting.MemoryRecallWait:
			// Return to inheritance.
		default:
			next = setting.MemoryRecallFast
		}
		p.err = setting.SaveMemoryRecallMode(cwd, scope, next)
		if p.err == nil {
			p.err = svc.Reload(cwd)
			if p.err == nil && next != "" && svc.MemoryRecall().Mode != next {
				p.err = fmt.Errorf("saved, but effective lookup is %s", recallModeLabel(svc.MemoryRecall().Mode))
			}
		}
	case memoryTimeout:
		p.input = textinput.New()
		p.input.Placeholder = "Inherit (e.g. 5s to override)"
		p.input.CharLimit = 64
		p.input.SetValue(p.timeouts[i])
		p.input.Focus()
		p.editing = true
		return
	}
	p.load(cwd, svc)
}

func (p *autoMemoryPanel) handleTimeoutKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		p.editing, p.err = false, nil
	case "enter":
		cwd, svc := p.workspace()
		if svc == nil {
			p.err = fmt.Errorf("settings unavailable")
			return nil
		}
		scope := memoryScopes[p.cursor/memoryRowCount].scope
		value := strings.TrimSpace(p.input.Value())
		p.err = setting.SaveMemoryRecallTimeout(cwd, scope, value)
		if p.err != nil {
			return nil
		}
		p.editing = false
		p.err = svc.Reload(cwd)
		if duration, err := time.ParseDuration(value); p.err == nil && err == nil && svc.MemoryRecall().Timeout != duration {
			p.err = fmt.Errorf("saved, but effective timeout is %s", svc.MemoryRecall().Timeout)
		}
		p.load(cwd, svc)
	default:
		var cmd tea.Cmd
		p.input, cmd = p.input.Update(msg)
		return cmd
	}
	return nil
}

func recallModeLabel(mode setting.MemoryRecallMode) string {
	switch mode {
	case setting.MemoryRecallFast:
		return "Fast"
	case setting.MemoryRecallWait:
		return "Wait"
	case "":
		return "inherit"
	default:
		return "invalid"
	}
}

func (p *autoMemoryPanel) Render(width, _ int) string {
	muted := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	focus := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Focus).Bold(true)
	var b strings.Builder
	b.WriteString(muted.Render(p.status) + "\n\n")
	if p.editing {
		b.WriteString(memoryScopes[p.cursor/memoryRowCount].label + " wait timeout\n" + p.input.View())
	} else {
		for i, scope := range memoryScopes {
			enabled, timeout := "inherit", p.timeouts[i]
			if p.values[i] != nil {
				enabled = onOff(*p.values[i])
			}
			if timeout == "" {
				timeout = "inherit"
			}
			for j, row := range [...]struct{ label, value string }{
				{"enabled", enabled}, {"lookup", recallModeLabel(p.modes[i])}, {"wait timeout", timeout},
			} {
				label := scope.label + " " + row.label
				cursor := "  "
				if i*memoryRowCount+j == p.cursor {
					cursor = focus.Render("▸ ")
				}
				gap := max(1, width-2-lipgloss.Width(label)-lipgloss.Width(row.value))
				b.WriteString(cursor + label + strings.Repeat(" ", gap) + muted.Render(row.value) + "\n")
			}
		}
		b.WriteString("\n" + muted.Render("Fast: start now · Wait: wait up to timeout"))
		b.WriteString("\n" + muted.Render("Browse files and stores with /memory."))
	}
	if p.err != nil {
		b.WriteString("\n" + lipgloss.NewStyle().Foreground(kit.CurrentTheme.Error).Width(width).Render(p.err.Error()))
	}
	return b.String()
}
