package skill

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func writePersonaSkill(t *testing.T, base, name, body string) string {
	t.Helper()
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + name + " skill\n---\n" + body
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func activeFullNames(r *Registry) []string {
	active := r.GetActive()
	out := make([]string, len(active))
	for i, s := range active {
		out[i] = s.FullName()
	}
	return out
}

func TestLoadPersona_AddsSkillsActiveByDefault(t *testing.T) {
	base := t.TempDir()
	d1 := writePersonaSkill(t, base, "lit-review", "do a lit review")
	d2 := writePersonaSkill(t, base, "run-experiment", "run an experiment")

	r := NewRegistry()
	r.LoadPersona([]string{d1, d2}, map[string]string{"run-experiment": "enable"})

	active := activeFullNames(r)
	if !slices.Contains(active, "lit-review") {
		t.Errorf("lit-review should be active by default; active=%v", active)
	}
	if slices.Contains(active, "run-experiment") {
		t.Errorf("run-experiment was set to enable, should not be active; active=%v", active)
	}
	if !r.IsEnabled("run-experiment") {
		t.Error("run-experiment should be enabled (slash-visible)")
	}
	if sk, ok := r.Get("lit-review"); !ok || sk.Scope != ScopePersona {
		t.Errorf("persona skill should carry ScopePersona; got %+v", sk)
	}
	if r.Count() != 2 {
		t.Errorf("Count = %d, want 2", r.Count())
	}
}

func TestClearPersona_RemovesAndRestoresShadowed(t *testing.T) {
	base := t.TempDir()
	d := writePersonaSkill(t, base, "lit-review", "persona version")

	r := NewRegistry()
	global := &Skill{Name: "lit-review", Description: "global", Scope: ScopeUser, State: StateDisable}
	r.skills["lit-review"] = global

	r.LoadPersona([]string{d}, nil)
	if sk, _ := r.Get("lit-review"); sk == nil || sk.Scope != ScopePersona {
		t.Fatalf("persona skill should shadow the global; got %+v", sk)
	}

	r.ClearPersona()
	sk, ok := r.Get("lit-review")
	if !ok || sk != global {
		t.Errorf("ClearPersona should restore the shadowed global; got %+v", sk)
	}
	if sk.State != StateDisable {
		t.Errorf("restored global state = %q, want disable", sk.State)
	}
}

func TestClearPersona_RemovesAddedWithoutShadow(t *testing.T) {
	base := t.TempDir()
	d := writePersonaSkill(t, base, "solo", "")

	r := NewRegistry()
	r.LoadPersona([]string{d}, nil)
	r.ClearPersona()

	if _, ok := r.Get("solo"); ok {
		t.Error("added persona skill should be removed on clear")
	}
	if r.Count() != 0 {
		t.Errorf("Count = %d, want 0", r.Count())
	}
}

func TestLoadPersona_ReplacesPrevious(t *testing.T) {
	base := t.TempDir()
	da := writePersonaSkill(t, base, "alpha", "")
	db := writePersonaSkill(t, base, "beta", "")

	r := NewRegistry()
	r.LoadPersona([]string{da}, nil)
	r.LoadPersona([]string{db}, nil) // second selection replaces the first

	if _, ok := r.Get("alpha"); ok {
		t.Error("first persona's skill 'alpha' should be gone after switching")
	}
	if _, ok := r.Get("beta"); !ok {
		t.Error("second persona's skill 'beta' should be present")
	}
	if r.Count() != 1 {
		t.Errorf("Count = %d, want 1", r.Count())
	}
}

func TestLoadPersona_EmptyDirsIsNoop(t *testing.T) {
	r := NewRegistry()
	r.LoadPersona(nil, nil)
	if r.Count() != 0 {
		t.Errorf("Count = %d, want 0", r.Count())
	}
	r.ClearPersona() // safe with no persona loaded
}
