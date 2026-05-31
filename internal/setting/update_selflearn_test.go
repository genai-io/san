package setting

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// userSettingsFile returns the ~/.gen/settings.json path under a HOME that
// the caller has already pointed at a temp dir.
func userSettingsFile(home string) string {
	return filepath.Join(home, ".gen", "settings.json")
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
		Memory: SelfLearnMemory{Enabled: true, EveryTurns: 7},
		Skills: SelfLearnSkills{Enabled: true},
	}, true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if sl := read(); !sl.Memory.Enabled || !sl.Skills.Enabled {
		t.Fatalf("after enable, want both on, got %+v", sl)
	}

	if err := UpdateSelfLearnAt(SelfLearnSettings{
		Memory: SelfLearnMemory{Enabled: false, EveryTurns: 7},
		Skills: SelfLearnSkills{Enabled: false},
	}, true); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if sl := read(); sl.Memory.Enabled || sl.Skills.Enabled {
		t.Fatalf("disable was dropped (OR-merge regression): %+v", sl)
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
	if err := os.WriteFile(file, []byte(`{"model":"claude-x","theme":"dark"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := UpdateSelfLearnAt(SelfLearnSettings{Memory: SelfLearnMemory{Enabled: true}}, true); err != nil {
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
}
