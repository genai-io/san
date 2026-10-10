package setting

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// userSettingsFile returns the ~/.san/settings.json path under a HOME that
// the caller has already pointed at a temp dir.
func userSettingsFile(home string) string {
	return filepath.Join(home, ".san", "settings.json")
}

// TestUpdateSelfLearnAtPersistsDisable guards the regression where disabling
// an arm via /config was silently dropped. UpdateSelfLearnAt used to route
// through the OR-merge save path, so once an arm was enabled on disk it
// could never be turned off (false || true = true). The off-toggle must now
// persist.
func TestUpdateSelfLearnAtPersistsDisable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	file := userSettingsFile(home)

	read := func() SelfLearnSettings {
		t.Helper()
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read user settings: %v", err)
		}
		var d Data
		if err := json.Unmarshal(data, &d); err != nil {
			t.Fatalf("unmarshal user settings: %v", err)
		}
		return d.SelfLearn
	}

	if err := UpdateSelfLearnAt(SelfLearnSettings{
		Memory: SelfLearnMemory{Enabled: true},
		Skills: SelfLearnSkills{}, // all actions allowed → skills active
	}, ScopeUser); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if sl := read(); !sl.Memory.Enabled || !sl.Skills.Active() {
		t.Fatalf("after enable, want both on, got %+v", sl)
	}

	if err := UpdateSelfLearnAt(SelfLearnSettings{
		Memory: SelfLearnMemory{Enabled: false},
		Skills: SelfLearnSkills{DenyCreate: true, DenyUpdate: true, DenyDelete: true}, // all denied → off
	}, ScopeUser); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if sl := read(); sl.Memory.Enabled || sl.Skills.Active() {
		t.Fatalf("disable was dropped (OR-merge regression): %+v", sl)
	}
}

func TestUpdateSelfLearnAtSyncsEvolveTool(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	file := userSettingsFile(home)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(`{"disabledTools":null}`), 0o644); err != nil {
		t.Fatal(err)
	}
	denied := SelfLearnSkills{DenyCreate: true, DenyUpdate: true, DenyDelete: true}
	for _, tc := range []struct {
		name     string
		cfg      SelfLearnSettings
		disabled bool
	}{
		{"memory", SelfLearnSettings{Memory: SelfLearnMemory{Enabled: true}, Skills: denied}, false},
		{"off", SelfLearnSettings{Skills: denied}, true},
		{"skills", SelfLearnSettings{}, false},
		{"off again", SelfLearnSettings{Skills: denied}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := UpdateSelfLearnAt(tc.cfg, ScopeUser); err != nil {
				t.Fatal(err)
			}
			d, err := NewLoader().LoadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if disabled, ok := d.DisabledTools["Evolve"]; !ok || disabled != tc.disabled {
				t.Fatalf("Evolve disabled = %v (explicit: %v), want %v", disabled, ok, tc.disabled)
			}
			if WithDefaultDisabledTools(d.DisabledTools)["Evolve"] != tc.disabled {
				t.Fatal("saved toggle did not override the Evolve factory default")
			}
		})
	}
}

// TestUpdateLastOperationMode confirms the user-wide startup preference is
// written without changing unrelated settings.
func TestUpdateLastOperationMode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	file := userSettingsFile(home)

	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(`{"model":"claude-x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := UpdateLastOperationMode(ModeBypassPermissions); err != nil {
		t.Fatalf("update: %v", err)
	}

	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var d Data
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	if d.LastOperationMode != "bypass" {
		t.Fatalf("LastOperationMode = %q, want bypass", d.LastOperationMode)
	}
	if d.Model != "claude-x" {
		t.Fatalf("unrelated setting clobbered: model=%q", d.Model)
	}
}

// TestUpdateSelfLearnAtPreservesOtherSettings confirms replacing the
// selfLearn block leaves unrelated settings in the same file intact.
func TestUpdateSelfLearnAtPreservesOtherSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	file := userSettingsFile(home)

	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(`{"model":"claude-x","theme":"dark","disabledTools":{"Evolve":true,"Bash":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := UpdateSelfLearnAt(SelfLearnSettings{Memory: SelfLearnMemory{Enabled: true}}, ScopeUser); err != nil {
		t.Fatalf("update: %v", err)
	}

	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var d Data
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	if d.Model != "claude-x" || d.Theme != "dark" {
		t.Fatalf("unrelated settings clobbered: model=%q theme=%q", d.Model, d.Theme)
	}
	if !d.SelfLearn.Memory.Enabled {
		t.Fatalf("selfLearn not written: %+v", d.SelfLearn)
	}
	if d.DisabledTools["Evolve"] || !d.DisabledTools["Bash"] {
		t.Fatalf("Evolve should be enabled without changing other tools: %+v", d.DisabledTools)
	}
}
