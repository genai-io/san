package input

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestIndexOfTheme(t *testing.T) {
	cases := map[string]int{
		"dark":  0,
		"light": 1,
		"auto":  2,
		"":      0, // unknown / unset falls back to the first choice
		"bogus": 0,
	}
	for in, want := range cases {
		if got := indexOfTheme(in); got != want {
			t.Errorf("indexOfTheme(%q) = %d, want %d", in, got, want)
		}
	}
}

// TestAppearancePanelDirty confirms Dirty tracks the hovered row against the
// saved baseline: false on the current theme, true once the cursor moves off.
func TestAppearancePanelDirty(t *testing.T) {
	p := newAppearancePanel(nil)
	p.Enter() // nil settings → baseline "auto" (index 2), cursor there too
	if p.Dirty() {
		t.Fatalf("fresh panel parked on the saved theme should not be dirty")
	}
	p.HandleKey(tea.KeyMsg{Type: tea.KeyUp}) // auto → light
	if !p.Dirty() {
		t.Fatalf("after moving off the baseline the panel should be dirty")
	}
	p.HandleKey(tea.KeyMsg{Type: tea.KeyDown}) // light → auto
	if p.Dirty() {
		t.Fatalf("back on the baseline the panel should not be dirty")
	}
}

// TestAppearancePanelEnterSavesAndEmits confirms enter persists the hovered
// theme to the user settings file, advances the baseline, and emits a
// ThemeSavedMsg so the app can confirm + reload.
func TestAppearancePanelEnterSavesAndEmits(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	p := newAppearancePanel(nil)
	p.Enter()                                // baseline "auto" (index 2)
	p.HandleKey(tea.KeyMsg{Type: tea.KeyUp}) // auto → light
	p.HandleKey(tea.KeyMsg{Type: tea.KeyUp}) // light → dark

	cmd, done := p.HandleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !done {
		t.Fatalf("enter should dismiss the popup (done=true)")
	}
	if p.baseline != "dark" {
		t.Fatalf("baseline = %q, want %q", p.baseline, "dark")
	}
	if p.saveErr != nil {
		t.Fatalf("unexpected saveErr: %v", p.saveErr)
	}
	if cmd == nil {
		t.Fatalf("expected a ThemeSavedMsg command")
	}
	msg, ok := cmd().(ThemeSavedMsg)
	if !ok || msg.Theme != "dark" {
		t.Fatalf("expected ThemeSavedMsg{dark}, got %#v", cmd())
	}

	raw, err := os.ReadFile(filepath.Join(home, ".san", "settings.json"))
	if err != nil {
		t.Fatalf("settings file not written: %v", err)
	}
	var data struct {
		Theme string `json:"theme"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("settings file not valid JSON: %v\n%s", err, raw)
	}
	if data.Theme != "dark" {
		t.Fatalf("persisted theme = %q, want %q\n%s", data.Theme, "dark", raw)
	}
}

// TestAppearancePanelEnterSaveFailureSurfacesError confirms a failed persist is
// surfaced (saveErr set, error shown in Render) and not silently swallowed: the
// panel stays open, the baseline is untouched, and no ThemeSavedMsg fires.
func TestAppearancePanelEnterSaveFailureSurfacesError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// Block the write: a regular file where the .san dir must be makes the
	// loader's MkdirAll fail.
	if err := os.WriteFile(filepath.Join(home, ".san"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := newAppearancePanel(nil)
	p.Enter()                                // baseline "auto"
	p.HandleKey(tea.KeyMsg{Type: tea.KeyUp}) // → light

	cmd, done := p.HandleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if done {
		t.Fatalf("a failed save should keep the popup open (done=false)")
	}
	if cmd != nil {
		t.Fatalf("a failed save should not emit ThemeSavedMsg, got %#v", cmd())
	}
	if p.saveErr == nil {
		t.Fatalf("a failed save should set saveErr")
	}
	if p.baseline != "auto" {
		t.Fatalf("baseline should be untouched on failure, got %q", p.baseline)
	}
	if out := p.Render(80); !strings.Contains(out, "couldn't save theme") {
		t.Fatalf("Render should surface the save error, got:\n%s", out)
	}
}

// TestConfigSelectorArrowSwitchesPanels confirms ←/→ cycle the registered
// panels (and wrap), the navigation this feature swapped in for ctrl-tab.
func TestConfigSelectorArrowSwitchesPanels(t *testing.T) {
	c := NewConfigSelector(nil)
	c.Enter(120, 40)
	if got := c.ActivePanel().Title(); got != "self-learning" {
		t.Fatalf("default panel = %q, want self-learning", got)
	}
	c.HandleKeypress(tea.KeyMsg{Type: tea.KeyRight})
	if got := c.ActivePanel().Title(); got != "appearance" {
		t.Fatalf("after right = %q, want appearance", got)
	}
	c.HandleKeypress(tea.KeyMsg{Type: tea.KeyRight}) // wrap
	if got := c.ActivePanel().Title(); got != "self-learning" {
		t.Fatalf("after right wrap = %q, want self-learning", got)
	}
	c.HandleKeypress(tea.KeyMsg{Type: tea.KeyLeft}) // wrap back
	if got := c.ActivePanel().Title(); got != "appearance" {
		t.Fatalf("after left wrap = %q, want appearance", got)
	}
}
