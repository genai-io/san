package setting

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPersonaAt_ReadsEachScopeRaw(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)

	if err := os.MkdirAll(filepath.Join(home, ".san"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".san", "settings.json"), []byte(`{"persona":"user-p"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cwd, ".san"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".san", "settings.json"), []byte(`{"persona":"proj-p"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := PersonaAt(cwd, false); got != "proj-p" {
		t.Errorf("project PersonaAt = %q, want proj-p", got)
	}
	if got := PersonaAt(cwd, true); got != "user-p" {
		t.Errorf("user PersonaAt = %q, want user-p", got)
	}

	// A project with no pin reads empty (so the switch falls back to user scope).
	bare := t.TempDir()
	if got := PersonaAt(bare, false); got != "" {
		t.Errorf("unpinned project PersonaAt = %q, want empty", got)
	}
}
