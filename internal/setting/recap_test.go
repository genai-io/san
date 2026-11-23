package setting

import (
	"path/filepath"
	"testing"
)

func TestRecapSettingsLayering(t *testing.T) {
	trustHome(t)
	cwd := t.TempDir()
	TrustWorkspaceForSession(cwd)
	userPath := filepath.Join(NewLoaderForCwd(cwd).dir(ScopeUser), "settings.json")
	if err := mutateSettingsPath(userPath, func(d *Data) {
		d.AutoPilot.RecapAfterMinutes = 12
		d.MemoryUpdate = new(true)
	}); err != nil {
		t.Fatal(err)
	}
	s := New(nil)
	check := func(want RecapSettings) {
		t.Helper()
		if err := s.Reload(cwd); err != nil {
			t.Fatal(err)
		}
		if got := s.Recap(); got != want || !s.MemoryUpdate() {
			t.Fatalf("Recap() = %+v, want %+v; MemoryUpdate = %t", got, want, s.MemoryUpdate())
		}
	}
	save := func(scope Scope, patch RecapSettings) {
		t.Helper()
		if err := SaveRecapSettings(cwd, scope, patch); err != nil {
			t.Fatal(err)
		}
	}
	check(RecapSettings{AfterMinutes: 12, Turns: 3})
	save(ScopeUser, RecapSettings{AfterMinutes: -1, Turns: 6})
	check(RecapSettings{AfterMinutes: -1, Turns: 6})
	save(ScopeProject, RecapSettings{AfterMinutes: 5})
	check(RecapSettings{AfterMinutes: 5, Turns: 6})
	save(ScopeProject, RecapSettings{Turns: 4})
	check(RecapSettings{AfterMinutes: 5, Turns: 4})
}
