package input

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/setting"
)

func TestAppearanceStatusBarsCompactLayout(t *testing.T) {
	p := newAppearancePanel(nil)
	p.Enter()
	var bars []appearanceOption
	for _, opt := range p.options {
		if isAppearanceBar(opt) {
			bars = append(bars, opt)
		}
	}
	if len(bars) != 2 || bars[0].section != bars[1].section {
		t.Fatal("the status bar region must contain exactly two independent controls")
	}
	for _, width := range []int{40, 70, 100} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			offColumn, onColumn := -1, -1
			for i, opt := range bars {
				row := renderBarToggle(opt, i == 1, width)
				plain := ansi.Strip(row)
				if lipgloss.Width(row) > width || strings.ContainsAny(row, "\r\n") {
					t.Fatalf("toggle must stay on one bounded row: %q", plain)
				}
				off := lipgloss.Width(plain[:strings.Index(plain, "Off")])
				on := lipgloss.Width(plain[:strings.Index(plain, "On")])
				if i == 0 {
					offColumn, onColumn = off, on
				} else if offColumn != off || onColumn != on {
					t.Fatalf("toggle cells are not aligned: Off %d/%d, On %d/%d", offColumn, off, onColumn, on)
				}
			}
		})
	}
	body := ansi.Strip(p.Render(100, 40))
	if strings.Count(body, "STATUS BAR") != 1 || strings.Contains(body, "CONTEXT BAR") || strings.Contains(body, "GIT BAR") {
		t.Fatalf("redundant status bar sections in Appearance: %q", body)
	}
}

func TestAppearanceBarPreviewDoesNotChangeOtherPreference(t *testing.T) {
	contextOn := true
	p := newAppearancePanel(setting.New(&setting.Data{ContextBar: &contextOn}))
	p.Enter()
	p.HandleKey(tea.KeyPressMsg{Code: tea.KeyDown}) // Auto -> Context usage
	if p.options[p.cursor].kind != kindContextBar || !p.options[p.cursor].barOn {
		t.Fatal("context toggle did not load the saved On value")
	}
	p.HandleKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	if !p.Dirty() || !p.barBaseline {
		t.Fatal("preview must not mutate the persisted baseline")
	}
	p.HandleKey(tea.KeyPressMsg{Code: tea.KeyDown}) // Context usage -> Git information
	if p.options[p.cursor].kind != kindGitBar || p.options[p.cursor].barOn || p.Dirty() {
		t.Fatal("context preview leaked into the Git toggle")
	}
	if !strings.Contains(ansi.Strip(p.HintLine()), "←→") {
		t.Fatal("compact toggle must advertise its Off/On keys")
	}
	p.HandleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	if !p.Dirty() || p.gitBarBaseline {
		t.Fatal("Git preview must not mutate the persisted baseline")
	}
	p.HandleKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if !p.options[p.cursor].barOn || p.Dirty() {
		t.Fatal("returning to a row must show its saved value, not an abandoned preview")
	}
	p.HandleKey(tea.KeyPressMsg{Code: tea.KeyUp}) // Context usage -> Auto
	cursor := p.cursor
	p.HandleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	if p.cursor != cursor || p.Dirty() {
		t.Fatal("toggle keys must not affect radio choices")
	}
}

func TestAppearanceBothBarsVisibleOnOpen(t *testing.T) {
	for _, theme := range []string{"dark", "light", "auto"} {
		popup := NewSettingsSelector(setting.New(&setting.Data{Theme: theme}))
		popup.Enter(80, 24)
		frame := popup.Render()
		plain := ansi.Strip(frame)
		for _, label := range []string{"Context usage", "Git information"} {
			if !strings.Contains(plain, label) {
				t.Fatalf("theme %s: %s is hidden on first open", theme, label)
			}
		}
		if lipgloss.Height(frame) > 24 || lipgloss.Width(frame) > 80 {
			t.Fatalf("theme %s: panel exceeds the terminal", theme)
		}
		// Reach Git information without applying or persisting any preference.
		p := popup.ActivePanel().(*appearancePanel)
		for p.cursor < len(p.options)-1 && p.options[p.cursor].kind != kindGitBar {
			popup.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyDown})
		}
		if p.options[p.cursor].kind != kindGitBar {
			t.Fatal("Git information row is missing")
		}
		hint := popup.renderHint(popup.ActivePanel().HintLine())
		if strings.Contains(hint, "\n") || lipgloss.Width(hint) > popup.innerWidth() {
			t.Fatalf("toggle hint would wrap: %q", ansi.Strip(hint))
		}
	}
}

func TestAppearanceRadioDescriptionsAlign(t *testing.T) {
	column := -1
	for _, label := range []string{"Dark", "Light", "Auto", "Full", "Collapsed", "Hidden"} {
		row := ansi.Strip(renderRadioRow(label, "DESCRIPTION", false, false))
		start := strings.Index(row, "DESCRIPTION")
		got := lipgloss.Width(row[:start])
		if column < 0 {
			column = got
		} else if got != column {
			t.Fatalf("%s description starts at %d, want %d", label, got, column)
		}
	}
}

func TestAppearanceBodyRecordsPositions(t *testing.T) {
	p := newAppearancePanel(nil)
	p.Enter()
	for i, opt := range p.options {
		p.cursor = i
		body := p.renderBody(70)
		if row := ansi.Strip(body.lines[body.cursorRow]); !strings.Contains(row, opt.label) || !strings.HasPrefix(row, "▸ ") {
			t.Fatalf("cursor metadata does not point to %s: %q", opt.label, row)
		}
		if heading := ansi.Strip(body.lines[body.sectionRow]); !strings.HasPrefix(heading, opt.section) {
			t.Fatalf("section metadata does not point to %s: %q", opt.section, heading)
		}
		if len(body.errors) != 0 {
			t.Fatal("body recorded error rows without a save error")
		}
	}
	p.saveErr = errors.New("save failed")
	body := p.renderBody(70)
	if !strings.Contains(ansi.Strip(strings.Join(body.errors, "\n")), "save failed") {
		t.Fatal("save error is not recorded separately for pinning")
	}
	if strings.Contains(strings.Join(body.lines, "\n"), "save failed") {
		t.Fatal("save error leaked into the scrollable option rows")
	}
}
