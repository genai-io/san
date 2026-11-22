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
	press(tea.KeyDown) // User lookup
	press(tea.KeyEnter)
	if mode, _ := setting.MemoryRecallAt(cwd, setting.ScopeUser); mode != setting.MemoryRecallFast {
		t.Fatalf("explicit Fast not saved: %q", mode)
	}
	press(tea.KeyEnter) // Wait
	if svc.MemoryRecall().Mode != setting.MemoryRecallWait || !strings.Contains(p.Render(60, 20), "Wait · 5s") {
		t.Fatalf("expected effective Wait with default timeout: %s", p.status)
	}
	press(tea.KeyDown) // User wait timeout
	press(tea.KeyEnter)
	typeText("0s")
	press(tea.KeyEnter)
	if !p.Modal() || p.err == nil || svc.MemoryRecall().Timeout != 5*time.Second {
		t.Fatal("invalid timeout should keep the editor open without saving")
	}
	press(tea.KeyEscape)
	if p.Modal() || p.err != nil {
		t.Fatal("Escape should cancel the edit and return to the rows")
	}
	press(tea.KeyEnter)
	typeText("750ms")
	press(tea.KeyEnter)
	if p.Modal() || p.err != nil || svc.MemoryRecall().Timeout != 750*time.Millisecond {
		t.Fatalf("timeout edit not saved: config = %+v, error = %v", svc.MemoryRecall(), p.err)
	}
	press(tea.KeyDown) // Project enablement
	press(tea.KeyDown) // Project lookup
	press(tea.KeyEnter)
	if svc.MemoryRecall().Mode != setting.MemoryRecallFast || !svc.AutoMemory() {
		t.Fatalf("project Fast should override user Wait without changing enablement: %+v", svc.MemoryRecall())
	}
	press(tea.KeyEnter) // Wait
	press(tea.KeyEnter) // inherit
	if mode, _ := setting.MemoryRecallAt(cwd, setting.ScopeProject); mode != "" || svc.MemoryRecall().Mode != setting.MemoryRecallWait {
		t.Fatalf("expected inherited Wait, got scoped %q, effective %+v", mode, svc.MemoryRecall())
	}
}
