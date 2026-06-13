package persona

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestParseDir_LoadsParts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ml-researcher")
	writeFile(t, filepath.Join(dir, "system", "identity.md"), "You are an ML researcher.\n")
	writeFile(t, filepath.Join(dir, "system", "behavior.md"), "  Discuss like a peer.  ")
	writeFile(t, filepath.Join(dir, "skills", "lit-review", "SKILL.md"), "---\nname: lit-review\n---\nbody\n")
	writeFile(t, filepath.Join(dir, "settings.json"), `{"description":"ML research","skills":{"lit-review":"active"}}`)

	p, ok := parseDir(dir)
	if !ok {
		t.Fatal("expected persona to load")
	}
	if p.Name != "ml-researcher" {
		t.Errorf("Name = %q", p.Name)
	}
	if p.Identity != "You are an ML researcher." {
		t.Errorf("Identity = %q", p.Identity)
	}
	if p.Behavior != "Discuss like a peer." {
		t.Errorf("Behavior = %q (want trimmed)", p.Behavior)
	}
	if p.Rules != "" {
		t.Errorf("Rules = %q, want empty (no rules.md)", p.Rules)
	}
	if len(p.SkillDirs) != 1 {
		t.Fatalf("SkillDirs = %v, want 1", p.SkillDirs)
	}
	if p.Description != "ML research" {
		t.Errorf("Description = %q", p.Description)
	}
	if p.Settings == nil || p.Settings.Skills["lit-review"] != "active" {
		t.Errorf("Settings.Skills not parsed: %+v", p.Settings)
	}
}

func TestParseDir_SkipsEmptyDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "empty")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := parseDir(dir); ok {
		t.Error("an empty directory should not be a persona")
	}
}

func TestParseDir_SettingsOnlyIsValid(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cfg-only")
	writeFile(t, filepath.Join(dir, "settings.json"), `{"description":"config overlay only"}`)
	p, ok := parseDir(dir)
	if !ok {
		t.Fatal("settings-only persona should load")
	}
	if p.Description != "config overlay only" {
		t.Errorf("Description = %q", p.Description)
	}
}

func TestParseDir_ToleratesBrokenSettings(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "broken")
	writeFile(t, filepath.Join(dir, "system", "identity.md"), "You are X.\n")
	writeFile(t, filepath.Join(dir, "settings.json"), `{ not valid json `)
	p, ok := parseDir(dir)
	if !ok {
		t.Fatal("persona with a system file should load despite broken settings.json")
	}
	if p.Settings != nil {
		t.Error("broken settings.json should yield nil Settings")
	}
}

func TestParseDir_OverlayFields(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "overlay")
	writeFile(t, filepath.Join(dir, "settings.json"),
		`{"description":"o","disabledTools":{"WebSearch":true},"permissions":{"allow":["Bash(pytest:*)"]}}`)
	p, ok := parseDir(dir)
	if !ok {
		t.Fatal("expected persona to load")
	}
	if !p.Settings.DisabledTools["WebSearch"] {
		t.Errorf("overlay disabledTools not parsed: %+v", p.Settings.DisabledTools)
	}
	if len(p.Settings.Permissions.Allow) != 1 || p.Settings.Permissions.Allow[0] != "Bash(pytest:*)" {
		t.Errorf("overlay permissions not parsed: %+v", p.Settings.Permissions)
	}
}

func TestRegistry_ProjectOverridesUser(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	writeFile(t, filepath.Join(home, ".san", "personas", "ml", "settings.json"),
		`{"description":"user-level ml"}`)
	writeFile(t, filepath.Join(cwd, ".san", "personas", "ml", "settings.json"),
		`{"description":"project-level ml"}`)

	r := NewRegistry(cwd)
	got, ok := r.Get("ml")
	if !ok {
		t.Fatal("expected ml persona to be registered")
	}
	if got.Scope != ScopeProject {
		t.Errorf("Scope = %v, want ScopeProject", got.Scope)
	}
	if got.Description != "project-level ml" {
		t.Errorf("Description = %q, want project-level", got.Description)
	}
}

func TestRegistry_ListDefaultFirst(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFile(t, filepath.Join(home, ".san", "personas", "zeta", "settings.json"), `{"description":"z"}`)

	r := NewRegistry("")
	list := r.List()
	if len(list) < 2 {
		t.Fatalf("expected default + zeta, got %d", len(list))
	}
	if list[0].Name != DefaultName || !list[0].IsBuiltin() {
		t.Errorf("first entry = %q, want builtin default", list[0].Name)
	}
}

func TestRegistry_LoadDirSkipsDefaultName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFile(t, filepath.Join(home, ".san", "personas", "default", "settings.json"), `{"description":"reserved"}`)

	r := NewRegistry("")
	got, _ := r.Get("default")
	if got == nil || !got.IsBuiltin() {
		t.Error("'default' must resolve to the virtual builtin, not a user directory")
	}
}

func TestSortPersonas_DefaultProjectUser(t *testing.T) {
	items := []*Persona{
		{Name: "z-user", Scope: ScopeUser},
		{Name: "a-project", Scope: ScopeProject},
		{Name: "default", Scope: ScopeBuiltin},
		{Name: "a-user", Scope: ScopeUser},
	}
	sortPersonas(items)
	want := []string{"default", "a-project", "a-user", "z-user"}
	for i, w := range want {
		if items[i].Name != w {
			t.Fatalf("pos %d = %q, want %q", i, items[i].Name, w)
		}
	}
}

func TestDefaultPersona_IsBuiltin(t *testing.T) {
	p := DefaultPersona()
	if !p.IsBuiltin() {
		t.Error("DefaultPersona should report IsBuiltin() == true")
	}
	if p.Identity != "" || p.Behavior != "" || p.Rules != "" {
		t.Error("DefaultPersona part overrides should be empty")
	}
}

func TestIsPersonaFile(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	inside := []string{
		filepath.Join(home, ".san", "personas", "ml", "system", "identity.md"),
		filepath.Join(cwd, ".san", "personas", "go", "settings.json"),
		filepath.Join(cwd, ".san", "personas", "go", "skills", "x", "SKILL.md"),
	}
	for _, p := range inside {
		if !IsPersonaFile(cwd, p) {
			t.Errorf("IsPersonaFile(%q) = false, want true", p)
		}
	}

	outside := []string{
		filepath.Join(home, ".san", "identities", "ml.md"),    // identities are no longer loaded
		filepath.Join(cwd, "personas", "go", "settings.json"), // not under .san
		"",
	}
	for _, p := range outside {
		if IsPersonaFile(cwd, p) {
			t.Errorf("IsPersonaFile(%q) = true, want false", p)
		}
	}
}

func TestEnsureUserDir_IsIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := EnsureUserDir(); err != nil {
		t.Fatalf("EnsureUserDir first call: %v", err)
	}
	readme := filepath.Join(home, ".san", "personas", "README.md")
	custom := []byte("user-edited README\n")
	if err := os.WriteFile(readme, custom, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := EnsureUserDir(); err != nil {
		t.Fatalf("EnsureUserDir second call: %v", err)
	}
	got, err := os.ReadFile(readme)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(custom) {
		t.Error("README should not be overwritten on the second call")
	}
}
