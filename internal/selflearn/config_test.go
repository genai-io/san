package selflearn

import (
	"strings"
	"testing"

	"github.com/genai-io/san/internal/setting"
)

// TestResolveSettingsHappyPath confirms a sensible config converts cleanly
// to a resolved Config and that defaults apply where fields are unset.
func TestResolveSettingsHappyPath(t *testing.T) {
	s := setting.SelfLearnSettings{
		Skills:   setting.SelfLearnSkills{}, // Deny* unset → all three actions allowed
		Strategy: "custom strategy",
	}
	r, err := ResolveSettings(s)
	if err != nil {
		t.Fatalf("happy path failed: %v", err)
	}
	wantSkills := AllowAllSkillActions()
	if r.Skills != wantSkills {
		t.Fatalf("skill permissions: got %+v, want %+v", r.Skills, wantSkills)
	}
	if r.Strategy != "custom strategy" {
		t.Fatalf("strategy override: got %q", r.Strategy)
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
	if !strings.Contains(err.Error(), `"Create new skills" needs "Update a skill"`) {
		t.Fatalf("error not from Validate: %v", err)
	}
}
