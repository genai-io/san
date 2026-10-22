package input

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/setting"
)

func TestAppearanceGitBarDefaultAndDirty(t *testing.T) {
	p := newAppearancePanel(nil)
	p.Enter()
	if p.gitBarBaseline {
		t.Fatal("Git bar must default off")
	}
	rows := 0
	for i, option := range p.options {
		if option.kind != kindGitBar {
			continue
		}
		rows++
		p.cursor = i
		if p.Dirty() || option.barOn {
			t.Fatal("Git information must start off and clean")
		}
		p.HandleKey(tea.KeyPressMsg{Code: tea.KeyRight})
		if !p.Dirty() || !p.options[i].barOn {
			t.Fatal("right should preview On and mark the preference dirty")
		}
		p.HandleKey(tea.KeyPressMsg{Code: tea.KeyLeft})
		if p.Dirty() || p.options[i].barOn {
			t.Fatal("left should return to the saved Off preference")
		}
	}
	if rows != 1 || strings.Count(p.Render(100, 40), "STATUS BAR") != 1 {
		t.Fatal("Appearance must group one Git toggle with the context toggle")
	}
}

func TestAppearanceGitBarReadsBaseline(t *testing.T) {
	on := true
	p := newAppearancePanel(setting.New(&setting.Data{GitBar: &on}))
	p.Enter()
	if !p.gitBarBaseline {
		t.Fatal("panel did not read the saved Git bar preference")
	}
	for _, opt := range p.options {
		if opt.kind == kindGitBar && (!opt.barOn || !p.isCurrent(opt)) {
			t.Fatal("toggle does not match the saved preference")
		}
	}
}

func TestAppearanceGitBarSavesAndEmits(t *testing.T) {
	tempHome(t)
	p := newAppearancePanel(nil)
	p.Enter()
	for _, on := range []bool{true, false} {
		for i, opt := range p.options {
			if opt.kind == kindGitBar {
				p.cursor = i
			}
		}
		key := tea.KeyLeft
		if on {
			key = tea.KeyRight
		}
		p.HandleKey(tea.KeyPressMsg{Code: key})
		cmd, done := p.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		if !done || cmd == nil || p.saveErr != nil || p.gitBarBaseline != on || p.Dirty() {
			t.Fatalf("on=%v: done=%v, err=%v, baseline=%v", on, done, p.saveErr, p.gitBarBaseline)
		}
		msg, ok := cmd().(GitBarSavedMsg)
		if !ok || msg.On != on {
			t.Fatalf("on=%v: wrong saved message %#v", on, msg)
		}
		controller := setting.New(setting.NewData())
		if err := controller.Reload(t.TempDir()); err != nil {
			t.Fatal(err)
		}
		if controller.Snapshot().ShowGitBar() != on {
			t.Fatalf("on=%v did not persist", on)
		}
	}
}

func TestAppearanceGitBarSaveFailure(t *testing.T) {
	home := tempHome(t)
	if err := os.WriteFile(filepath.Join(home, ".san"), []byte("blocked"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := newAppearancePanel(nil)
	p.Enter()
	cmd, done := p.apply(appearanceOption{kind: kindGitBar, barOn: true})
	if cmd != nil || done || p.saveErr == nil || p.gitBarBaseline {
		t.Fatalf("failed save: done=%v, err=%v, baseline=%v", done, p.saveErr, p.gitBarBaseline)
	}
	if !strings.Contains(p.Render(100, 8), "couldn't save") || lipgloss.Height(p.Render(100, 8)) > 8 {
		t.Fatal("short panel must keep the save error visible without overflowing")
	}
}

func TestAppearanceOptionsStayVisibleInShortPanel(t *testing.T) {
	p := newAppearancePanel(nil)
	p.Enter()
	for i, opt := range p.options {
		p.cursor = i
		body := p.Render(70, 8)
		if lipgloss.Height(body) > 8 {
			t.Fatalf("row %d: body exceeded available height: %q", i, body)
		}
		plain := ansi.Strip(body)
		hovered := strings.Contains(plain, "▸ ● "+opt.label) || strings.Contains(plain, "▸ ○ "+opt.label)
		if isAppearanceBar(opt) || opt.kind == kindBashPreview {
			hovered = strings.Contains(plain, "▸ "+opt.label)
		}
		if !strings.Contains(plain, opt.section) || !hovered {
			t.Fatalf("row %d: current section/option scrolled out of view: %q", i, plain)
		}
		for _, line := range strings.Split(body, "\n") {
			if lipgloss.Width(line) > 70 {
				t.Fatalf("row %d: a description exceeded available width", i)
			}
		}
	}
	popup := NewSettingsSelector(nil)
	popup.Enter(80, 24)
	if got := lipgloss.Height(popup.Render()); got > 24 {
		t.Fatalf("Appearance popup height = %d, exceeds a 24-row terminal", got)
	}
}
