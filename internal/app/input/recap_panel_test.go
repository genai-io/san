package input

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/setting"
)

func TestRecapPanelEditAndReopen(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	svc := setting.New(nil)
	popup := NewReflectionSelector(ReflectionDeps{Workspace: func() (string, *setting.Settings) { return cwd, svc }})
	popup.Enter(100, 30)
	press := func(code rune) { popup.HandleKeypress(tea.KeyPressMsg{Code: code}) }
	press(tea.KeyTab)
	press(tea.KeyTab)
	p, ok := popup.ActivePanel().(*recapPanel)
	if !ok {
		t.Fatalf("expected Recap as the third tab, got %s", popup.ActivePanel().Title())
	}
	check := func(want setting.RecapSettings) {
		t.Helper()
		if got := svc.Recap(); got != want || p.config != want {
			t.Fatalf("effective settings = %+v, panel = %+v, want %+v (error: %v)", got, p.config, want, p.err)
		}
	}
	replace := func(text string) {
		t.Helper()
		for range p.input.Value() {
			press(tea.KeyBackspace)
		}
		for _, r := range text {
			popup.HandleKeypress(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
	}
	want := setting.RecapSettings{AfterMinutes: 5, Turns: 3}
	check(want)
	press(tea.KeyEnter)
	press(tea.KeyTab) // The modal captures Tab.
	if popup.ActivePanel() != p || !p.Modal() {
		t.Fatal("Tab should not leave the numeric editor")
	}
	for _, invalid := range []string{"0", "-2", "1.5", ""} {
		replace(invalid)
		press(tea.KeyEnter)
		if !p.Modal() || p.err == nil {
			t.Fatalf("invalid time away %q should keep the editor open with an error", invalid)
		}
		check(want)
	}
	replace("-1")
	press(tea.KeyEnter)
	want.AfterMinutes = -1
	check(want)
	if p.Modal() || p.err != nil || !strings.Contains(popup.Render(), "Off (-1)") {
		t.Fatalf("saving -1 should show auto recap as off: %s", popup.Render())
	}

	press(tea.KeyDown)
	press(tea.KeyDown) // The turns row is the last row.
	for _, text := range []string{"Time away (minutes)", "Turns between recaps", "Completed user turns", "since session start"} {
		if view := popup.Render(); !strings.Contains(view, text) {
			t.Fatalf("Recap panel missing %q: %s", text, view)
		}
	}
	press(tea.KeyEnter)
	replace("-1")
	press(tea.KeyEnter)
	if !p.Modal() || p.err == nil {
		t.Fatal("-1 should not be accepted for a turn count")
	}
	replace("11")
	press(tea.KeyEscape)
	if !popup.IsActive() || p.Modal() || p.err != nil {
		t.Fatal("Escape should cancel the edit without closing the popup")
	}
	check(want)
	press(tea.KeyEnter)
	replace("11")
	press(tea.KeyEnter)
	want.Turns = 11
	check(want)

	press(tea.KeyEscape)
	svc = setting.New(nil)
	if err := svc.Reload(cwd); err != nil {
		t.Fatal(err)
	}
	popup.Enter(100, 30)
	if popup.ActivePanel() != p || p.Modal() || p.cursor != recapAfter || p.err != nil {
		t.Fatal("reopening should return to the Recap list")
	}
	check(want)
}
