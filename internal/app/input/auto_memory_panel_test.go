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
	press(tea.KeyEnter)
	typeText("750ms")
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
