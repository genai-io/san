package setting

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestAutoSkillWireCompatibility(t *testing.T) {
	disabled := AutoSkillPermissions{DenyCreate: true, DenyUpdate: true, DenyDelete: true}
	for _, tc := range []struct {
		name, js, yml string
		want          AutoSkillSettings
	}{
		{"absent", `{}`, `{}`, AutoSkillSettings{}},
		{"legacy disabled", `{"selfLearn":{"skills":{},"strategy":"old"}}`, "selfLearn:\n  skills: {}\n  strategy: old", AutoSkillSettings{Skills: disabled, Strategy: "old"}},
		{"canonical", `{"autoSkill":{"skills":{"enabled":true,"denyDelete":true},"strategy":"new"}}`, "autoSkill:\n  skills: {enabled: true, denyDelete: true}\n  strategy: new", AutoSkillSettings{Skills: AutoSkillPermissions{DenyDelete: true}, Strategy: "new"}},
		{"both", `{"autoSkill":{"strategy":"new"},"selfLearn":{"skills":{},"strategy":"old"}}`, "autoSkill: {strategy: new}\nselfLearn: {skills: {}, strategy: old}", AutoSkillSettings{Strategy: "new"}},
		{"canonical empty", `{"selfLearn":{"skills":{}},"autoSkill":{}}`, "selfLearn: {skills: {}}\nautoSkill: {}", AutoSkillSettings{}},
		{"canonical null", `{"selfLearn":{"skills":{}},"autoSkill":null}`, "selfLearn: {skills: {}}\nautoSkill: null", AutoSkillSettings{}},
	} {
		for _, codec := range []struct {
			name, raw string
			read      func([]byte, any) error
			write     func(any) ([]byte, error)
		}{
			{"JSON", tc.js, json.Unmarshal, json.Marshal},
			{"YAML", tc.yml, yaml.Unmarshal, yaml.Marshal},
		} {
			t.Run(tc.name+"/"+codec.name, func(t *testing.T) {
				var d Data
				if err := codec.read([]byte(codec.raw), &d); err != nil {
					t.Fatal(err)
				}
				if d.AutoSkill != tc.want || New(&d).AutoSkill() != tc.want {
					t.Fatalf("Auto Skill = %+v, want %+v", d.AutoSkill, tc.want)
				}
				encoded, err := codec.write(d)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(encoded), "selfLearn") || (tc.want != (AutoSkillSettings{}) && !strings.Contains(string(encoded), "autoSkill")) {
					t.Fatalf("incorrect key on write: %s", encoded)
				}
				var roundTrip Data
				if err := codec.read(encoded, &roundTrip); err != nil || roundTrip.AutoSkill != tc.want {
					t.Fatalf("round trip = %+v, err = %v; want %+v", roundTrip.AutoSkill, err, tc.want)
				}
			})
		}
	}
}

func TestReviewSkillMigratesSavedToolRules(t *testing.T) {
	var got Data
	raw := `{"disabledTools":{"Evolve":true,"ReviewSkill":false},"permissions":{"deny":["Evolve","Evolve(*)","OtherTool"]}}`
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if disabled, found := got.DisabledTools[reviewSkillTool]; !found || disabled || len(got.DisabledTools) != 1 {
		t.Fatalf("canonical explicit tool preference was lost: %v", got.DisabledTools)
	}
	if strings.Join(got.Permissions.Deny, ",") != "ReviewSkill,ReviewSkill(*),OtherTool" {
		t.Fatalf("renamed tool escaped its existing deny rules: %v", got.Permissions.Deny)
	}
}

func TestAutoSkillMixedLayersAndSaveMigration(t *testing.T) {
	user, project := t.TempDir(), t.TempDir()
	userPath := filepath.Join(user, "settings.json")
	legacy := `{"selfLearn":{"strategy":"user","skills":{}},"disabledTools":{"Evolve":true}}`
	for path, contents := range map[string]string{
		userPath:                                legacy,
		filepath.Join(project, "settings.json"): `{"autoSkill":{"strategy":"project","skills":{"enabled":true}}}`,
	} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	loader := NewLoaderWithOptions(user, project, false)
	got, err := loader.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.AutoSkill.Strategy != "project" || got.AutoSkill.Skills.Active() || !got.DisabledTools[reviewSkillTool] {
		t.Fatalf("layering lost strategy or restrictions: %+v, disabledTools=%v", got.AutoSkill, got.DisabledTools)
	}
	if raw, err := os.ReadFile(userPath); err != nil || string(raw) != legacy {
		t.Fatalf("load rewrote legacy file: %s, err=%v", raw, err)
	}
	if err := loader.SaveToUser(&Data{Theme: "dark"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"autoSkill"`) || strings.Contains(string(raw), `"selfLearn"`) {
		t.Fatalf("expected canonical key after save: %s", raw)
	}
	saved, err := loader.LoadFile(userPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.AutoSkill.Strategy != "user" || saved.AutoSkill.Skills.Active() || !saved.DisabledTools[reviewSkillTool] || saved.Theme != "dark" {
		t.Fatalf("migration lost settings: %+v", saved)
	}
}
