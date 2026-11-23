package input

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/setting"
)

const (
	recapAfter = iota
	recapTurns
)

var recapRows = [...]struct {
	label string
	help  string
}{
	{"Time away (minutes)", "-1: no auto recap; /recap still works."},
	{"Turns between recaps", "Completed user turns since the last recap, or since session start."},
}

type recapPanel struct {
	workspace func() (string, *setting.Settings)
	config    setting.RecapSettings
	available bool
	cursor    int
	editing   bool
	input     textinput.Model
	err       error
}

func (p *recapPanel) Title() string { return "Recap" }
func (p *recapPanel) Dirty() bool   { return false }
func (p *recapPanel) Modal() bool   { return p.editing }

func (p *recapPanel) Breadcrumb() string {
	if p.editing {
		return recapRows[p.cursor].label
	}
	return ""
}

func (p *recapPanel) HintLine() string {
	if p.editing {
		return keycap("enter") + " save  " + keycap("esc") + " cancel"
	}
	return keycap("↑↓") + "  " + keycap("enter") + " edit"
}

func (p *recapPanel) Enter() {
	p.cursor, p.editing, p.err = 0, false, nil
	p.available = false
	if p.workspace != nil {
		if _, svc := p.workspace(); svc != nil {
			p.available = true
			p.config = svc.Recap()
		}
	}
}

func (p *recapPanel) values() [len(recapRows)]int {
	return [...]int{p.config.AfterMinutes, p.config.Turns}
}

func (p *recapPanel) HandleKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	if p.editing {
		switch msg.String() {
		case "esc":
			p.input.Blur()
			p.editing, p.err = false, nil
		case "enter":
			p.save()
		default:
			var cmd tea.Cmd
			p.input, cmd = p.input.Update(msg)
			return cmd, false
		}
		return nil, false
	}

	switch msg.String() {
	case "up", "k":
		p.cursor = max(0, p.cursor-1)
	case "down", "j":
		p.cursor = min(len(recapRows)-1, p.cursor+1)
	case "enter":
		if p.available {
			p.input = textinput.New()
			p.input.SetValue(strconv.Itoa(p.values()[p.cursor]))
			p.input.CursorEnd()
			p.editing, p.err = true, nil
			return p.input.Focus(), false
		}
	}
	return nil, false
}

func (p *recapPanel) save() {
	value, err := strconv.Atoi(strings.TrimSpace(p.input.Value()))
	if err != nil || (value <= 0 && !(p.cursor == recapAfter && value == -1)) {
		p.err = fmt.Errorf("enter a positive integer")
		if p.cursor == recapAfter {
			p.err = fmt.Errorf("enter a positive integer or -1")
		}
		return
	}
	cwd, svc := p.workspace()
	if svc == nil {
		p.err = fmt.Errorf("settings unavailable")
		return
	}
	var patch setting.RecapSettings
	switch p.cursor {
	case recapAfter:
		patch.AfterMinutes = value
	case recapTurns:
		patch.Turns = value
	}
	p.err = setting.SaveRecapSettings(cwd, memorySettingsScope(cwd), patch)
	if p.err != nil {
		return
	}
	p.err = svc.Reload(cwd)
	p.config = svc.Recap()
	if effective := p.values()[p.cursor]; p.err == nil && effective != value {
		p.err = fmt.Errorf("saved, but the effective value is still %d", effective)
	}
	p.input.Blur()
	p.editing = false
}

func (p *recapPanel) Render(width, height int) string {
	width, height = max(width, 1), max(height, 1)
	muted := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	if !p.available {
		return muted.Render(kit.TruncateText("Settings unavailable", width))
	}
	help := muted.Render(kit.TruncateText(recapRows[p.cursor].help, width))
	var lines []string
	if p.editing {
		in := p.input
		in.SetWidth(max(1, width-2))
		lines = append(lines, in.View())
	} else {
		budget := height
		if p.err != nil && budget > 1 {
			budget--
		}
		visible := min(len(recapRows), budget)
		start := min(max(0, p.cursor-visible+1), len(recapRows)-visible)
		values := p.values()
		for i := start; i < start+visible; i++ {
			value := strconv.Itoa(values[i])
			if i == recapAfter && values[i] == -1 {
				value = "Off (-1)"
			}
			lines = append(lines, renderMemoryPanelRow(memoryPanelRow{label: recapRows[i].label, value: value}, width, i == p.cursor, false))
		}
	}
	if p.err != nil && len(lines) < height {
		lines = append(lines, memoryPanelError(p.err, width))
	}
	if len(lines) < height {
		lines = append(lines, help)
	}
	return strings.Join(lines, "\n")
}
