package setting

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMemoryUpdatePersistenceAndTrust(t *testing.T) {
	trustHome(t)
	cwd := t.TempDir()
	s := New(nil)
	check := func(want bool) {
		t.Helper()
		if err := s.Reload(cwd); err != nil {
			t.Fatal(err)
		}
		if got := s.MemoryUpdate(); got != want {
			t.Fatalf("MemoryUpdate() = %v, want %v", got, want)
		}
	}
	save := func(scope Scope, enabled bool) {
		t.Helper()
		if err := SaveMemoryUpdate(cwd, scope, enabled); err != nil {
			t.Fatal(err)
		}
	}

	check(false)
	if err := SaveMemoryRecallMode(cwd, ScopeUser, MemoryRecallWait); err != nil {
		t.Fatal(err)
	}
	save(ScopeProject, true)
	check(false) // Untrusted project settings cannot opt in.
	if got := strings.Join(ProjectSettingsFindings(cwd), "\n"); !strings.Contains(got, "automatically update memory") {
		t.Fatalf("missing project opt-in review finding: %q", got)
	}
	TrustWorkspaceForSession(cwd)
	check(true)
	save(ScopeUser, true)
	check(true)
	if s.MemoryRecall().Mode != MemoryRecallWait {
		t.Fatal("saving Update lost Recall settings")
	}
	*s.Snapshot().MemoryUpdate = false
	if !s.MemoryUpdate() {
		t.Fatal("snapshot mutation changed live settings")
	}
	save(ScopeProject, false)
	check(false) // Explicit project false overrides the user opt-in.
	projectPath := filepath.Join(NewLoaderForCwd(cwd).dir(ScopeProject), "settings.json")
	raw, err := os.ReadFile(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || string(fields["memoryUpdate"]) != "false" {
		t.Fatalf("expected explicit JSON boolean false: %s (error: %v)", raw, err)
	}
	if err := os.Remove(projectPath); err != nil {
		t.Fatal(err)
	}
	check(true) // An absent project setting inherits the user opt-in.
}

func TestMemoryUpdateIgnoresLegacyMaintain(t *testing.T) {
	trustHome(t)
	cwd := t.TempDir()
	path := filepath.Join(NewLoaderForCwd(cwd).dir(ScopeUser), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	const legacy = `{"memoryMaintain":{"add":true,"update":true,"forget":true}}`
	if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	s := New(nil)
	if err := s.Reload(cwd); err != nil {
		t.Fatal(err)
	}
	if s.MemoryUpdate() || s.Snapshot().MemoryUpdate != nil {
		t.Fatal("legacy Maintain must not opt in to Update")
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != legacy {
		t.Fatalf("reload rewrote legacy settings: %s (error: %v)", raw, err)
	}
}
