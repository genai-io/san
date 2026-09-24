package input

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/setting"
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
	p.HandleKey(tea.KeyPressMsg{Code: tea.KeyUp}) // auto → light
	if !p.Dirty() {
		t.Fatalf("after moving off the baseline the panel should be dirty")
	}
	p.HandleKey(tea.KeyPressMsg{Code: tea.KeyDown}) // light → auto
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
	p.Enter()                                     // baseline "auto" (index 2)
	p.HandleKey(tea.KeyPressMsg{Code: tea.KeyUp}) // auto → light
	p.HandleKey(tea.KeyPressMsg{Code: tea.KeyUp}) // light → dark

	cmd, done := p.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !done {
		t.Fatalf("enter should dismiss the popup (done=true)")
	}
	if p.themeBaseline != "dark" {
		t.Fatalf("themeBaseline = %q, want %q", p.themeBaseline, "dark")
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
	p.Enter()                                     // baseline "auto"
	p.HandleKey(tea.KeyPressMsg{Code: tea.KeyUp}) // → light

	cmd, done := p.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if done {
		t.Fatalf("a failed save should keep the popup open (done=false)")
	}
	if cmd != nil {
		t.Fatalf("a failed save should not emit ThemeSavedMsg, got %#v", cmd())
	}
	if p.saveErr == nil {
		t.Fatalf("a failed save should set saveErr")
	}
	if p.themeBaseline != "auto" {
		t.Fatalf("themeBaseline should be untouched on failure, got %q", p.themeBaseline)
	}
	if out := p.Render(80, 20); !strings.Contains(out, "couldn't save") {
		t.Fatalf("Render should surface the save error, got:\n%s", out)
	}
}

// TestAppearancePanelContextBarSavesAndEmits confirms selecting the "On"
// context-bar row persists contextBar=true to the user settings file,
// advances the bar baseline, and emits a ContextBarSavedMsg.
func TestAppearancePanelContextBarSavesAndEmits(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	p := newAppearancePanel(nil)
	p.Enter() // cursor parks on the current theme (index 2, "auto")
	if p.barBaseline {
		t.Fatalf("context bar should default off")
	}

	// Walk down to the "On" context-bar row (index 3) and select it.
	p.HandleKey(tea.KeyPressMsg{Code: tea.KeyDown}) // auto → On
	cmd, done := p.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !done {
		t.Fatalf("enter should dismiss the popup (done=true)")
	}
	if !p.barBaseline {
		t.Fatalf("barBaseline should be true after enabling")
	}
	msg, ok := cmd().(ContextBarSavedMsg)
	if !ok || !msg.On {
		t.Fatalf("expected ContextBarSavedMsg{On:true}, got %#v", cmd())
	}

	raw, err := os.ReadFile(filepath.Join(home, ".san", "settings.json"))
	if err != nil {
		t.Fatalf("settings file not written: %v", err)
	}
	var data struct {
		ContextBar *bool `json:"contextBar"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("settings file not valid JSON: %v\n%s", err, raw)
	}
	if data.ContextBar == nil || !*data.ContextBar {
		t.Fatalf("persisted contextBar = %v, want true\n%s", data.ContextBar, raw)
	}
}

// TestSettingsSelectorTabSwitchesPanels confirms tab / shift+tab cycle
// /settings's panels (and wrap). The shell's tab switching was dormant while
// /settings hosted a single panel; registering Permissions alongside
// Appearance puts it back in play.
func TestSettingsSelectorTabSwitchesPanels(t *testing.T) {
	c := NewSettingsSelector(nil)
	c.Enter(120, 40)
	if got := c.ActivePanel().Title(); got != "appearance" {
		t.Fatalf("default panel = %q, want appearance", got)
	}
	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyTab})
	if got := c.ActivePanel().Title(); got != "permissions" {
		t.Fatalf("after tab = %q, want permissions", got)
	}
	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyTab}) // wrap
	if got := c.ActivePanel().Title(); got != "appearance" {
		t.Fatalf("after tab wrap = %q, want appearance", got)
	}
	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}) // wrap back
	if got := c.ActivePanel().Title(); got != "permissions" {
		t.Fatalf("after shift+tab wrap = %q, want permissions", got)
	}
}

// The THINKING group is the reasoning-display preference. It defaults to full,
// so the preference is opt-in and an existing user's transcript is unchanged
// until they ask for it — an unset value must not silently start hiding
// reasoning.
func TestAppearancePanelThinkingGroupDefaultsToFull(t *testing.T) {
	var rows []appearanceOption
	for _, opt := range appearanceOptions() {
		if opt.kind == kindThinkingDisplay {
			rows = append(rows, opt)
		}
	}
	if len(rows) != 3 {
		t.Fatalf("THINKING should offer three choices, got %d", len(rows))
	}
	values := map[string]bool{}
	for _, opt := range rows {
		values[opt.thinkingDisplay] = true
	}
	for _, want := range []string{
		setting.ThinkingDisplayFull,
		setting.ThinkingDisplayCollapsed,
		setting.ThinkingDisplayHidden,
	} {
		if !values[want] {
			t.Errorf("THINKING is missing the %q row", want)
		}
	}

	p := newAppearancePanel(nil)
	p.Enter()
	if p.thinkingBaseline != setting.DefaultThinkingDisplay {
		t.Fatalf("baseline = %q, want the default %q",
			p.thinkingBaseline, setting.DefaultThinkingDisplay)
	}
	if p.thinkingBaseline != setting.ThinkingDisplayFull {
		t.Fatalf("the default must be full, got %q", p.thinkingBaseline)
	}
}

// Selecting a THINKING row persists it to the user settings file and emits
// ThinkingDisplaySavedMsg so the app can update its live render flag.
func TestAppearancePanelSavesThinkingDisplay(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	p := newAppearancePanel(nil)
	p.Enter()
	// Park on the Hidden row, wherever the group sits in the flat list.
	target := -1
	for i, opt := range p.options {
		if opt.kind == kindThinkingDisplay && opt.thinkingDisplay == setting.ThinkingDisplayHidden {
			target = i
		}
	}
	if target < 0 {
		t.Fatal("no Hidden row in the appearance options")
	}
	p.cursor = target

	cmd, done := p.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !done {
		t.Fatal("enter should dismiss the popup (done=true)")
	}
	if p.saveErr != nil {
		t.Fatalf("unexpected saveErr: %v", p.saveErr)
	}
	if cmd == nil {
		t.Fatal("expected a ThinkingDisplaySavedMsg command")
	}
	msg, ok := cmd().(ThinkingDisplaySavedMsg)
	if !ok || msg.Mode != setting.ThinkingDisplayHidden {
		t.Fatalf("expected ThinkingDisplaySavedMsg{hidden}, got %#v", cmd())
	}
	if p.thinkingBaseline != setting.ThinkingDisplayHidden {
		t.Fatalf("baseline = %q, want hidden", p.thinkingBaseline)
	}

	raw, err := os.ReadFile(filepath.Join(home, ".san", "settings.json"))
	if err != nil {
		t.Fatalf("settings file not written: %v", err)
	}
	var data struct {
		ThinkingDisplay string `json:"thinkingDisplay"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("settings file not valid JSON: %v\n%s", err, raw)
	}
	if data.ThinkingDisplay != setting.ThinkingDisplayHidden {
		t.Fatalf("persisted thinkingDisplay = %q, want hidden\n%s", data.ThinkingDisplay, raw)
	}
}
