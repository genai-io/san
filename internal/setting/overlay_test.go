package setting

import (
	"slices"
	"testing"
)

func TestApplyPersonaOverlay_OverridesAndTightens(t *testing.T) {
	base := &Data{
		Persona:       "ml-researcher",
		Model:         "claude-sonnet-4-6",
		DisabledTools: map[string]bool{"WebSearch": true, "Bash": false},
		Permissions: PermissionSettings{
			Deny:  []string{"Bash(rm -rf:*)"},
			Allow: []string{"Bash(ls:*)"},
		},
	}
	overlay := &Data{
		Persona:       "should-be-ignored",
		Model:         "claude-opus-4-8",
		DisabledTools: map[string]bool{"WebSearch": false, "HeavyTool": true},
		Permissions: PermissionSettings{
			Deny:  []string{"Bash(curl:*)"},
			Allow: []string{"Bash(pytest:*)"},
		},
	}

	got := ApplyPersonaOverlay(base, overlay)

	// The active-persona selector is NOT changed by the overlay (no re-select).
	if got.Persona != "ml-researcher" {
		t.Errorf("Persona = %q, want ml-researcher (overlay must not re-select)", got.Persona)
	}
	// Scalars: overlay wins.
	if got.Model != "claude-opus-4-8" {
		t.Errorf("Model = %q, want the overlay's", got.Model)
	}
	// disabledTools: per-key override — persona re-enables WebSearch, adds HeavyTool.
	if got.DisabledTools["WebSearch"] {
		t.Error("WebSearch should be re-enabled (false) by the overlay")
	}
	if !got.DisabledTools["HeavyTool"] {
		t.Error("HeavyTool should be disabled by the overlay")
	}
	// permissions: union — the base deny survives, the overlay deny is added.
	if !slices.Contains(got.Permissions.Deny, "Bash(rm -rf:*)") ||
		!slices.Contains(got.Permissions.Deny, "Bash(curl:*)") {
		t.Errorf("Deny = %v, want union of base+overlay", got.Permissions.Deny)
	}
	if !slices.Contains(got.Permissions.Allow, "Bash(ls:*)") ||
		!slices.Contains(got.Permissions.Allow, "Bash(pytest:*)") {
		t.Errorf("Allow = %v, want union of base+overlay", got.Permissions.Allow)
	}

	// Base must not be mutated.
	if base.Model != "claude-sonnet-4-6" || base.DisabledTools["WebSearch"] != true {
		t.Error("ApplyPersonaOverlay must not mutate the base settings")
	}
}

func TestApplyPersonaOverlay_NilOverlayReturnsBase(t *testing.T) {
	base := &Data{Persona: "x", Model: "m"}
	if got := ApplyPersonaOverlay(base, nil); got != base {
		t.Error("a nil overlay should return base unchanged")
	}
}
