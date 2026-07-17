package setting

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfLearnValidate exercises the two rejected boolean combinations and
// the maxKB invariant from notes/active/l1-background-review.md §3.1 / §4.2.
// The all-zero "feature off" baseline and several legal combinations must
// validate without error.
func TestSelfLearnValidate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cfg     SelfLearnSettings
		wantErr string // empty = expect ok; otherwise substring of the error
	}{
		{
			name:    "all zero (feature off)",
			cfg:     SelfLearnSettings{},
			wantErr: "",
		},
		{
			name: "explicit defaults",
			cfg: SelfLearnSettings{
				Memory: SelfLearnMemory{Enabled: true, MaxKB: 25},
			},
			wantErr: "",
		},
		{
			name: "freeze mode (deny create; update + delete default-allow)",
			cfg: SelfLearnSettings{
				Skills: SelfLearnSkills{DenyCreate: true},
			},
			wantErr: "",
		},
		{
			name: "patch-only mode (deny create + delete)",
			cfg: SelfLearnSettings{
				Skills: SelfLearnSkills{DenyCreate: true, DenyDelete: true},
			},
			wantErr: "",
		},
		{
			name: "rejected: create allowed but update denied",
			cfg: SelfLearnSettings{
				Skills: SelfLearnSkills{DenyUpdate: true},
			},
			wantErr: `"Create new skills" needs "Update a skill"`,
		},
		{
			name: "ok: create + update allowed but delete denied (no-auto-delete mode)",
			cfg: SelfLearnSettings{
				Skills: SelfLearnSkills{DenyDelete: true},
			},
			wantErr: "",
		},
		{
			name: "rejected: maxKB above injection cap",
			cfg: SelfLearnSettings{
				Memory: SelfLearnMemory{MaxKB: 26},
			},
			wantErr: "memory size must be between",
		},
		{
			name: "rejected: maxKB negative",
			cfg: SelfLearnSettings{
				Memory: SelfLearnMemory{MaxKB: -5},
			},
			wantErr: "memory size must be between",
		},
		{
			name: "ok: maxKB at lower-than-default",
			cfg: SelfLearnSettings{
				Memory: SelfLearnMemory{MaxKB: 10},
			},
			wantErr: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("expected ok, got %v", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("error mismatch: got %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestSelfLearnAllowAccessors confirms the Deny-encoded fields read out as
// the expected Allow* boolean — zero ⇒ allow, true ⇒ deny — for every
// permission. The contract every downstream consumer (skill.go,
// prompts.go) depends on.
func TestSelfLearnAllowAccessors(t *testing.T) {
	zero := SelfLearnSkills{}
	if !zero.AllowCreate() || !zero.AllowUpdate() || !zero.AllowDelete() {
		t.Fatal("zero-valued Deny fields must read as Allow=true")
	}
	denied := SelfLearnSkills{DenyCreate: true, DenyUpdate: true, DenyDelete: true}
	if denied.AllowCreate() || denied.AllowUpdate() || denied.AllowDelete() {
		t.Fatal("explicit Deny must read as Allow=false")
	}
}

func TestSelfLearnSkillsMigratesLegacyDisabledJSON(t *testing.T) {
	for _, raw := range []string{
		`{}`,
		`{"enabled":false}`,
		`{"denyDelete":true}`,
	} {
		var got SelfLearnSkills
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatalf("unmarshal %s: %v", raw, err)
		}
		if got.Active() {
			t.Errorf("legacy disabled config %s became active: %+v", raw, got)
		}
	}
}

func TestLoaderPreservesLegacySkillOptOut(t *testing.T) {
	userDir := t.TempDir()
	projectDir := t.TempDir()
	path := filepath.Join(userDir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"selfLearn":{"skills":{}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := NewLoaderWithOptions(userDir, projectDir, false).Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.SelfLearn.Skills.Active() {
		t.Fatalf("legacy loader path re-enabled opted-out skills: %+v", got.SelfLearn.Skills)
	}
}

func TestSelfLearnSkillsJSONRoundTripMarksCurrentSchema(t *testing.T) {
	want := SelfLearnSkills{DenyDelete: true}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"enabled":true`) {
		t.Fatalf("current schema marker missing from %s", data)
	}

	var got SelfLearnSkills
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
}

func TestSelfLearnSkillsMigratesLegacyEnabledJSON(t *testing.T) {
	var got SelfLearnSkills
	if err := json.Unmarshal([]byte(`{"enabled":true,"denyDelete":true}`), &got); err != nil {
		t.Fatal(err)
	}
	want := SelfLearnSkills{DenyDelete: true}
	if got != want {
		t.Fatalf("legacy enabled config = %+v, want %+v", got, want)
	}
}

// TestSelfLearnMemoryResolvedMaxKB confirms the default fallback for MaxKB when
// unset (zero) and identity for non-zero values.
func TestSelfLearnMemoryResolvedMaxKB(t *testing.T) {
	if got := (SelfLearnMemory{}).ResolvedMaxKB(); got != SelfLearnMaxMemoryKB {
		t.Fatalf("default MaxKB: got %d, want %d", got, SelfLearnMaxMemoryKB)
	}
	if got := (SelfLearnMemory{MaxKB: 10}).ResolvedMaxKB(); got != 10 {
		t.Fatalf("explicit MaxKB: got %d, want 10", got)
	}
}
