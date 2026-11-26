package input

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/setting"
)

const (
	memoryRecall = iota
	memoryUpdate
	memoryActionCount
)

const (
	memoryEnabled = iota
	memoryWaitTimeout
	memoryPreviewLines
	memoryPreviewSize
	memoryOptionCount
)

type memoryPage uint8

const (
	memoryActionsPage memoryPage = iota
	memoryOptionsPage
	memoryEditPage
)

type autoMemoryPanel struct {
	workspace func() (string, *setting.Settings)
	page      memoryPage
	cursor    int
	option    int
	available bool
	enabled   bool
	blocked   bool
	recall    setting.MemoryRecallConfig
	preview   setting.MemoryPreviewLimits
	update    bool
	err       error
	input     textinput.Model
}

func (p *autoMemoryPanel) Title() string { return "Auto Memory" }
func (p *autoMemoryPanel) Dirty() bool   { return false }
func (p *autoMemoryPanel) Modal() bool   { return p.page != memoryActionsPage }

func (p *autoMemoryPanel) Breadcrumb() string {
	switch p.page {
	case memoryOptionsPage:
		return "Options"
	case memoryEditPage:
		switch p.option {
		case memoryPreviewLines:
			return "Preview lines"
		case memoryPreviewSize:
			return "Preview size"
		default:
			return "Wait timeout"
		}
	default:
		return ""
	}
}

func (p *autoMemoryPanel) HintLine() string {
	switch p.page {
	case memoryEditPage:
		return keycap("enter") + " save  " + keycap("esc") + " cancel"
	case memoryOptionsPage:
		action := "toggle"
		if p.option != memoryEnabled {
			action = "edit"
		}
		return keycap("↑↓") + "  " + keycap("enter") + " " + action + "  " + keycap("esc") + " back"
	default:
		action := "toggle"
		if p.cursor == memoryRecall {
			action = "cycle"
		}
		return keycap("↑↓") + "  " + keycap("enter") + " " + action + "  " + keycap("→") + " options"
	}
}

func (p *autoMemoryPanel) Enter() {
	p.page, p.cursor, p.option, p.err = memoryActionsPage, 0, 0, nil
	if p.workspace != nil {
		_, svc := p.workspace()
		p.load(svc)
	}
}

func (p *autoMemoryPanel) load(svc *setting.Settings) {
	p.available = svc != nil
	if svc == nil {
		p.enabled, p.recall, p.update = false, setting.MemoryRecallConfig{}, false
		return
	}
	p.enabled = svc.AutoMemory()
	p.blocked = setting.AutoMemoryDisabledByEnv()
	p.recall = svc.MemoryRecall()
	p.preview = svc.MemoryPreview()
	p.update = svc.MemoryUpdate()
}

func (p *autoMemoryPanel) HandlePaste(content string) {
	if p.page == memoryEditPage {
		p.input, _ = p.input.Update(tea.PasteMsg{Content: content})
	}
}

func (p *autoMemoryPanel) HandleKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	if p.page == memoryEditPage {
		return p.handleEditKey(msg), false
	}
	key := msg.String()
	if p.page == memoryOptionsPage {
		switch key {
		case "esc", "left":
			p.page, p.err = memoryActionsPage, nil
		case "up", "k":
			p.option = max(0, p.option-1)
		case "down", "j":
			p.option = min(memoryOptionCount-1, p.option+1)
		case "enter":
			return p.activateOption(), false
		}
		return nil, false
	}
	switch key {
	case "up", "k":
		p.cursor = max(0, p.cursor-1)
	case "down", "j":
		p.cursor = min(memoryActionCount-1, p.cursor+1)
	case "right":
		p.page, p.option, p.err = memoryOptionsPage, memoryEnabled, nil
		if p.cursor == memoryRecall && p.recall.Mode == setting.MemoryRecallWait {
			p.option = memoryWaitTimeout
		}
	case "enter":
		p.activateAction()
	}
	return nil, false
}

func (p *autoMemoryPanel) activateAction() {
	if p.workspace == nil {
		return
	}
	cwd, svc := p.workspace()
	p.load(svc)
	if !p.available {
		return
	}
	scope := memorySettingsScope(cwd)
	mode := p.recall.Mode
	var enabled bool
	if p.cursor == memoryRecall {
		switch mode {
		case setting.MemoryRecallFast:
			mode = setting.MemoryRecallWait
		case setting.MemoryRecallWait:
			mode = setting.MemoryRecallOff
		default:
			mode = setting.MemoryRecallFast
		}
		p.err = setting.SaveMemoryRecallMode(cwd, scope, mode)
	} else {
		enabled = !p.update
		p.err = setting.SaveMemoryUpdate(cwd, scope, enabled)
	}
	if p.err == nil {
		p.err = svc.Reload(cwd)
	}
	p.load(svc)
	if p.err != nil {
		return
	}
	if p.cursor == memoryRecall {
		if p.recall.Mode != mode {
			p.err = fmt.Errorf("saved, but Recall is still %s", recallModeLabel(p.recall.Mode))
		}
	} else if p.update != enabled {
		p.err = fmt.Errorf("saved, but Update is still %s", onOff(p.update))
	}
}

func (p *autoMemoryPanel) activateOption() tea.Cmd {
	if p.workspace == nil {
		return nil
	}
	cwd, svc := p.workspace()
	p.load(svc)
	if !p.available {
		return nil
	}
	scope := memorySettingsScope(cwd)
	p.err = nil
	if p.option == memoryEnabled {
		p.err = setAutoMemory(svc, cwd, !p.enabled, scope)
		p.load(svc)
		return nil
	}
	p.input = textinput.New()
	switch p.option {
	case memoryPreviewLines, memoryPreviewSize:
		p.input.CharLimit = 64
		overrides := setting.MemoryPreviewAt(cwd, scope)
		value := overrides.Lines
		if p.option == memoryPreviewSize {
			value = overrides.KB
		}
		if value != 0 {
			p.input.SetValue(strconv.Itoa(value))
		}
	default:
		_, timeout := setting.MemoryRecallAt(cwd, scope)
		p.input.Placeholder = "e.g. 5s"
		p.input.SetValue(timeout)
	}
	p.page = memoryEditPage
	return p.input.Focus()
}

func (p *autoMemoryPanel) handleEditKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		p.input.Blur()
		p.page, p.err = memoryOptionsPage, nil
	case "enter":
		cwd, svc := p.workspace()
		if svc == nil {
			p.err = fmt.Errorf("settings unavailable")
			return nil
		}
		value := strings.TrimSpace(p.input.Value())
		scope := memorySettingsScope(cwd)
		var number int
		if p.option == memoryWaitTimeout {
			p.err = setting.SaveMemoryRecallTimeout(cwd, scope, value)
		} else {
			if value != "" {
				n, err := strconv.Atoi(value)
				if err != nil || n <= 0 {
					p.err = fmt.Errorf("enter a positive whole number or leave blank to inherit")
					return nil
				}
				number = n
			}
			if p.option == memoryPreviewLines {
				p.err = setting.SaveMemoryPreviewLines(cwd, scope, number)
			} else {
				p.err = setting.SaveMemoryPreviewKB(cwd, scope, number)
			}
		}
		if p.err != nil {
			return nil
		}
		p.err = svc.Reload(cwd)
		p.load(svc)
		if p.err == nil {
			switch p.option {
			case memoryWaitTimeout:
				if duration, err := time.ParseDuration(value); err == nil && p.recall.Timeout != duration {
					p.err = fmt.Errorf("saved, but Wait still uses %s", p.recall.Timeout)
				}
			case memoryPreviewLines:
				if number != 0 && p.preview.Lines != number {
					p.err = fmt.Errorf("saved, but the preview still uses %d lines", p.preview.Lines)
				}
			case memoryPreviewSize:
				if number != 0 && p.preview.KB != number {
					p.err = fmt.Errorf("saved, but the preview still uses %d KB", p.preview.KB)
				}
			}
		}
		p.input.Blur()
		p.page = memoryOptionsPage
	default:
		var cmd tea.Cmd
		p.input, cmd = p.input.Update(msg)
		return cmd
	}
	return nil
}

func recallModeLabel(mode setting.MemoryRecallMode) string {
	switch mode {
	case setting.MemoryRecallOff:
		return "Off"
	case setting.MemoryRecallWait:
		return "Wait"
	default:
		return "Fast"
	}
}
