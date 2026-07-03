package setting

import (
	"testing"
)

func TestNextWithBypass_Disabled(t *testing.T) {
	cycle := []OperationMode{ModeNormal, ModeAutoAccept, ModeAutoReview, ModeNormal}
	for i := 0; i < len(cycle)-1; i++ {
		got := cycle[i].NextWithBypass(false)
		if got != cycle[i+1] {
			t.Errorf("NextWithBypass(false): from %d, got %d, want %d", cycle[i], got, cycle[i+1])
		}
	}
}

func TestNextWithBypass_Enabled(t *testing.T) {
	cycle := []OperationMode{ModeNormal, ModeAutoAccept, ModeAutoReview, ModeBypassPermissions, ModeNormal}
	for i := 0; i < len(cycle)-1; i++ {
		got := cycle[i].NextWithBypass(true)
		if got != cycle[i+1] {
			t.Errorf("NextWithBypass(true): from %d, got %d, want %d", cycle[i], got, cycle[i+1])
		}
	}
}

func TestNextWithBypass_UnknownMode(t *testing.T) {
	unknown := OperationMode(99)
	if got := unknown.NextWithBypass(false); got != ModeNormal {
		t.Errorf("NextWithBypass(false) from unknown: got %d, want %d", got, ModeNormal)
	}
	if got := unknown.NextWithBypass(true); got != ModeNormal {
		t.Errorf("NextWithBypass(true) from unknown: got %d, want %d", got, ModeNormal)
	}
}

func TestNext_StillWorks(t *testing.T) {
	cycle := []OperationMode{ModeNormal, ModeAutoAccept, ModeAutoReview, ModeNormal}
	for i := 0; i < len(cycle)-1; i++ {
		got := cycle[i].Next()
		if got != cycle[i+1] {
			t.Errorf("Next(): from %d, got %d, want %d", cycle[i], got, cycle[i+1])
		}
	}
}

func TestNext_BypassReturnsNormal(t *testing.T) {
	got := ModeBypassPermissions.Next()
	if got != ModeNormal {
		t.Errorf("Next() from ModeBypassPermissions: got %d, want %d", got, ModeNormal)
	}
}

func TestAutoReview_StringAndFromString(t *testing.T) {
	if got := ModeAutoReview.String(); got != "auto review" {
		t.Errorf("ModeAutoReview.String() = %q, want %q", got, "auto review")
	}
	for _, s := range []string{"autoReview", "auto-review", "review"} {
		if got := OperationModeFromString(s); got != ModeAutoReview {
			t.Errorf("OperationModeFromString(%q) = %d, want %d", s, got, ModeAutoReview)
		}
	}
}
