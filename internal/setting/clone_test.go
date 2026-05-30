package setting

import "testing"

// TestClonePreservesAllScalarFields guards against the Clone() drift that
// caused identity to silently revert to default at startup: every scalar
// field on Data must round-trip through Clone. New fields should be
// added here at the same time they are added to Clone().
func TestClonePreservesAllScalarFields(t *testing.T) {
	yes := true
	src := &Data{
		Model:          "claude-opus-4-7",
		Theme:          "dark",
		SearchProvider: "exa",
		AllowBypass:    &yes,
		Identity:       "go-reviewer",
		SelfLearn: SelfLearnSettings{
			Memory: SelfLearnMemory{Enabled: true, EveryTurns: 7, MaxKB: 15},
			Skills: SelfLearnSkills{Enabled: true, DenyCreate: true, AllowUpdateUserCreated: true},
		},
	}

	dst := src.Clone()

	if dst.Model != src.Model {
		t.Errorf("Model: got %q, want %q", dst.Model, src.Model)
	}
	if dst.Theme != src.Theme {
		t.Errorf("Theme: got %q, want %q", dst.Theme, src.Theme)
	}
	if dst.SearchProvider != src.SearchProvider {
		t.Errorf("SearchProvider: got %q, want %q", dst.SearchProvider, src.SearchProvider)
	}
	if dst.AllowBypass == nil || *dst.AllowBypass != *src.AllowBypass {
		t.Errorf("AllowBypass: got %v, want %v", dst.AllowBypass, src.AllowBypass)
	}
	if dst.Identity != src.Identity {
		t.Errorf("Identity: got %q, want %q", dst.Identity, src.Identity)
	}
	// SelfLearn is value-typed; the whole struct (incl. nested Memory /
	// Skills) must survive. Skipping this row caused /config to silently
	// show stale defaults until the bug was caught.
	if dst.SelfLearn != src.SelfLearn {
		t.Errorf("SelfLearn: got %+v, want %+v", dst.SelfLearn, src.SelfLearn)
	}
}
