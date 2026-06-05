package selflearn

import (
	"strings"
	"testing"

	"github.com/genai-io/san/v2/internal/setting"
)

// TestResolveSettingsHappyPath confirms a sensible config converts cleanly
// to a resolved Config and that defaults apply where fields are unset.
func TestResolveSettingsHappyPath(t *testing.T) {
	s := setting.SelfLearnSettings{
		Memory: setting.SelfLearnMemory{Enabled: true, EveryTurns: 7}, // MaxKB unset → default
		Skills: setting.SelfLearnSkills{
			Enabled:        true,
			EveryToolIters: 15,
			// Allow* unset → default true
			AllowUpdateUserCreated: true,
		},
	}
	r, err := ResolveSettings(s)
	if err != nil {
		t.Fatalf("happy path failed: %v", err)
	}
	if !r.Memory.Enabled || r.Memory.Interval != 7 {
		t.Fatalf("memory arm: %+v", r.Memory)
	}
	if !r.Skills.Enabled || r.Skills.Interval != 15 {
		t.Fatalf("skill arm: %+v", r.Skills)
	}
	wantPerms := ActionPermissions{
		AllowCreate: true, AllowUpdate: true, AllowDelete: true,
		AllowUpdateUserCreated: true,
	}
	if r.Perms != wantPerms {
		t.Fatalf("perms: got %+v, want %+v", r.Perms, wantPerms)
	}
	if r.MemoryMaxChars != 25*1024 {
		t.Fatalf("memory cap: got %d, want %d", r.MemoryMaxChars, 25*1024)
	}
}

// TestResolveSettingsRejectsInvalid surfaces the underlying Validate error so
// the wire-up caller can refuse to start the reviewer on bad config.
func TestResolveSettingsRejectsInvalid(t *testing.T) {
	s := setting.SelfLearnSettings{
		Skills: setting.SelfLearnSkills{DenyUpdate: true}, // create is default-allow, so this is the illegal create-without-update combo
	}
	_, err := ResolveSettings(s)
	if err == nil {
		t.Fatal("expected validation error to propagate")
	}
	if !strings.Contains(err.Error(), `"Create new skills" needs "Update existing skills"`) {
		t.Fatalf("error not from Validate: %v", err)
	}
}

// TestResolveSettingsAppliesMaxKBDefault confirms an unset MaxKB resolves to
// the default cap (25 KB) and a lowered value passes through as bytes.
func TestResolveSettingsAppliesMaxKBDefault(t *testing.T) {
	defaultR, _ := ResolveSettings(setting.SelfLearnSettings{})
	if defaultR.MemoryMaxChars != 25*1024 {
		t.Fatalf("default MaxKB→bytes: %d", defaultR.MemoryMaxChars)
	}
	lowered, _ := ResolveSettings(setting.SelfLearnSettings{
		Memory: setting.SelfLearnMemory{MaxKB: 10},
	})
	if lowered.MemoryMaxChars != 10*1024 {
		t.Fatalf("explicit MaxKB→bytes: %d", lowered.MemoryMaxChars)
	}
}
