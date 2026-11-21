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

// TestUpdateAutoSkillAtPersistsDisable guards the regression where disabling
// an arm via /config was silently dropped. UpdateAutoSkillAt used to route
// through the OR-merge save path, so once an arm was enabled on disk it
// could never be turned off (false || true = true). The off-toggle must now
// persist.
func TestUpdateAutoSkillAtPersistsDisable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	file := userSettingsFile(home)

	read := func() AutoSkillSettings {
		t.Helper()
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read user settings: %v", err)
		}
		var d Data
		if err := json.Unmarshal(data, &d); err != nil {
			t.Fatalf("unmarshal user settings: %v", err)
		}
		return d.AutoSkill
	}

	if err := UpdateAutoSkillAt(AutoSkillSettings{
		Skills: AutoSkillPermissions{}, // all actions allowed → skills active
	}, ScopeUser); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if sl := read(); !sl.Skills.Active() {
		t.Fatalf("after enable, want skills on, got %+v", sl)
	}

	if err := UpdateAutoSkillAt(AutoSkillSettings{
		Skills: AutoSkillPermissions{DenyCreate: true, DenyUpdate: true, DenyDelete: true}, // all denied → off
	}, ScopeUser); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if sl := read(); sl.Skills.Active() {
		t.Fatalf("disable was dropped (OR-merge regression): %+v", sl)
	}
}

func TestUpdateAutoSkillAtSyncsEvolveTool(t *testing.T) {
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
	denied := AutoSkillPermissions{DenyCreate: true, DenyUpdate: true, DenyDelete: true}
	for _, tc := range []struct {
		name     string
		cfg      AutoSkillSettings
		disabled bool
	}{
		{"off", AutoSkillSettings{Skills: denied}, true},
		{"skills", AutoSkillSettings{}, false},
		{"off again", AutoSkillSettings{Skills: denied}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := UpdateAutoSkillAt(tc.cfg, ScopeUser); err != nil {
				t.Fatal(err)
			}
			d, err := NewLoader().LoadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if disabled, ok := d.DisabledTools[reviewSkillTool]; !ok || disabled != tc.disabled {
				t.Fatalf("Evolve disabled = %v (explicit: %v), want %v", disabled, ok, tc.disabled)
			}
			if WithDefaultDisabledTools(d.DisabledTools)[reviewSkillTool] != tc.disabled {
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

// TestUpdateAutoSkillAtPreservesOtherSettings confirms replacing the
// autoSkill block leaves unrelated settings in the same file intact.
func TestUpdateAutoSkillAtPreservesOtherSettings(t *testing.T) {
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

	if err := UpdateAutoSkillAt(AutoSkillSettings{Strategy: "custom"}, ScopeUser); err != nil {
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
	if d.AutoSkill.Strategy != "custom" {
		t.Fatalf("selfLearn not written: %+v", d.AutoSkill)
	}
	if d.DisabledTools[reviewSkillTool] || !d.DisabledTools["Bash"] {
		t.Fatalf("Evolve should be enabled without changing other tools: %+v", d.DisabledTools)
	}
}
