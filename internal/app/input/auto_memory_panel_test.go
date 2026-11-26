package input

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/setting"
)

func TestAutoMemoryPanelRecallKeys(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SAN_TRUST_WORKSPACE", "1")
	t.Setenv("SAN_DISABLE_AUTO_MEMORY", "")
	svc := setting.New(nil)
	popup := NewReflectionSelector(ReflectionDeps{Workspace: func() (string, *setting.Settings) { return cwd, svc }})
	popup.Enter(100, 30)
	p := popup.ActivePanel().(*autoMemoryPanel)
	press := func(code rune) { popup.HandleKeypress(tea.KeyPressMsg{Code: code}) }
	typeText := func(s string) {
		for _, r := range s {
			p.HandleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
	}
	press(tea.KeyEnter) // Fast → Wait
	if out := p.Render(60, 20); svc.MemoryRecall().Mode != setting.MemoryRecallWait || !strings.Contains(out, "Wait") || strings.Contains(out, "5s") {
		t.Fatalf("expected Wait without a timeout on the main page: %s", out)
	}
	press(tea.KeyRight) // Options, focused on Wait timeout.
	if out := p.Render(60, 20); !strings.Contains(out, "Wait timeout") || !strings.Contains(out, "5s") {
		t.Fatalf("expected timeout in Options: %s", out)
	}
	press(tea.KeyEnter)
	typeText("0s")
	press(tea.KeyEnter)
	if !p.Modal() || p.err == nil || svc.MemoryRecall().Timeout != 5*time.Second {
		t.Fatal("invalid timeout should keep the editor open without saving")
	}
	press(tea.KeyEscape)
	if p.page != memoryOptionsPage || p.err != nil {
		t.Fatal("Escape should cancel the edit and return to Options")
	}
	popup.HandlePaste("bad") // No editor is open.
	press(tea.KeyEnter)
	popup.HandlePaste("750ms")
	press(tea.KeyEnter)
	if p.page != memoryOptionsPage || p.err != nil || svc.MemoryRecall().Timeout != 750*time.Millisecond {
		t.Fatalf("timeout edit not saved: config = %+v, error = %v", svc.MemoryRecall(), p.err)
	}
	press(tea.KeyEscape) // Options → main rows, with Recall still selected.
	press(tea.KeyEnter)  // Wait → Off.
	if mode, _ := setting.MemoryRecallAt(cwd, setting.ScopeUser); mode != setting.MemoryRecallOff || !svc.AutoMemory() {
		t.Fatalf("Recall Off must be persisted without disabling the library: %q", mode)
	}
	press(tea.KeyEnter) // Off → Fast.
	if svc.MemoryRecall().Mode != setting.MemoryRecallFast || svc.MemoryRecall().Timeout != 750*time.Millisecond {
		t.Fatalf("switching modes should preserve the timeout: %+v", svc.MemoryRecall())
	}
}

func TestAutoMemoryPanelPreviewSettings(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SAN_TRUST_WORKSPACE", "1")
	svc := setting.New(nil)
	p := &autoMemoryPanel{workspace: func() (string, *setting.Settings) { return cwd, svc }}
	p.Enter()
	press := func(code rune) { p.HandleKey(tea.KeyPressMsg{Code: code}) }
	press(tea.KeyRight)
	if out := p.Render(75, 20); !strings.Contains(out, "Preview lines") || !strings.Contains(out, "200") || !strings.Contains(out, "25 KB") {
		t.Fatalf("default preview settings are not visible: %s", out)
	}
	press(tea.KeyDown) // Wait timeout
	press(tea.KeyDown) // Preview lines
	press(tea.KeyEnter)
	p.input.SetValue("80")
	press(tea.KeyEnter)
	press(tea.KeyDown) // Preview size
	press(tea.KeyEnter)
	p.input.SetValue("12")
	press(tea.KeyEnter)
	if got, want := svc.MemoryPreview(), (setting.MemoryPreviewLimits{Lines: 80, KB: 12}); got != want || p.err != nil {
		t.Fatalf("preview settings = %+v, want %+v; error = %v", got, want, p.err)
	}
	press(tea.KeyEnter)
	p.input.SetValue("")
	press(tea.KeyEnter)
	if got := svc.MemoryPreview(); got.Lines != 80 || got.KB != 25 || p.err != nil {
		t.Fatalf("clearing one override changed another: %+v, %v", got, p.err)
	}
}

func TestPreviewOverrideRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name   string
		option int
	}{{"lines", memoryPreviewLines}, {"size", memoryPreviewSize}} {
		t.Run(tc.name, func(t *testing.T) {
			home, cwd := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			svc := setting.New(nil)
			p := &autoMemoryPanel{workspace: func() (string, *setting.Settings) { return cwd, svc }}
			p.Enter()
			p.page, p.option = memoryOptionsPage, tc.option
			press := func(code rune) { p.HandleKey(tea.KeyPressMsg{Code: code}) }
			for _, step := range []struct {
				initial, draft string
				key            rune
			}{
				{"", "19", tea.KeyEnter}, {"19", "22", tea.KeyEscape}, {"19", "", tea.KeyEnter},
				{"", "21", tea.KeyEscape}, {"", "", tea.KeyEnter}, {"", "", tea.KeyEscape},
			} {
				press(tea.KeyEnter)
				if got := p.input.Value(); got != step.initial {
					t.Errorf("reopened with %q, want %q", got, step.initial)
				}
				if step.draft != step.initial || step.key != tea.KeyEnter {
					p.input.SetValue(step.draft)
				}
				press(step.key)
				if p.err != nil || p.page != memoryOptionsPage {
					t.Fatalf("finish edit: page %v error %v", p.page, p.err)
				}
			}
		})
	}
}

func TestAutoMemoryLongTimeoutRoundTrip(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	literal := strings.Repeat("1ms", 22)
	scope := memorySettingsScope(cwd)
	if err := setting.SaveMemoryRecallTimeout(cwd, scope, literal); err != nil {
		t.Fatal(err)
	}
	svc := setting.New(nil)
	p := &autoMemoryPanel{workspace: func() (string, *setting.Settings) { return cwd, svc }}
	p.Enter()
	p.page, p.option = memoryOptionsPage, memoryWaitTimeout
	for _, key := range []rune{tea.KeyEscape, tea.KeyEnter} {
		p.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		if got := p.input.Value(); got != literal {
			t.Errorf("editor duration = %q, want %q", got, literal)
		}
		p.HandleKey(tea.KeyPressMsg{Code: key})
	}
	if _, raw := setting.MemoryRecallAt(cwd, scope); raw != literal || p.err != nil || svc.MemoryRecall().Timeout != 22*time.Millisecond {
		t.Fatalf("saved %q, effective %s, error %v", raw, svc.MemoryRecall().Timeout, p.err)
	}
}

func TestAutoMemoryPanelUpdateKeys(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SAN_TRUST_WORKSPACE", "1")
	svc := setting.New(nil)
	popup := NewReflectionSelector(ReflectionDeps{Workspace: func() (string, *setting.Settings) { return cwd, svc }})
	popup.Enter(100, 30)
	p := popup.ActivePanel().(*autoMemoryPanel)
	press := func(code rune) { popup.HandleKeypress(tea.KeyPressMsg{Code: code}) }
	check := func(want bool) {
		t.Helper()
		if got := svc.MemoryUpdate(); got != want || p.err != nil || p.Dirty() {
			t.Fatalf("Update = %v, want %v; error = %v, dirty = %v", got, want, p.err, p.Dirty())
		}
	}
	check(false)
	press(tea.KeyDown)
	press(tea.KeyDown) // Update is the last row.
	if p.cursor != memoryUpdate {
		t.Fatalf("expected Update selected, got row %d", p.cursor)
	}
	press(tea.KeyEnter)
	check(true)
	out := p.Render(60, 20)
	for _, want := range []string{"Recall", "Update", "● On"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in panel: %s", want, out)
		}
	}
	for _, absent := range []string{"Remember", "Forget", "Wait timeout", "Auto Memory"} {
		if strings.Contains(out, absent) {
			t.Fatalf("unexpected %q on main page: %s", absent, out)
		}
	}
	press(tea.KeyRight)
	press(tea.KeyEnter) // Pause via master switch without clearing Update.
	if svc.AutoMemory() {
		t.Fatal("master switch did not pause Auto Memory")
	}
	check(true)
	press(tea.KeyEscape)
	press(tea.KeyEnter)
	check(false)
}
