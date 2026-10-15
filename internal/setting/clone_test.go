package setting

import "testing"

// TestClonePreservesAllScalarFields guards against Clone() drift that would
// silently revert a setting to its default at startup: every scalar field on
// Data must round-trip through Clone. New fields should be added here at the
// same time they are added to Clone().
func TestClonePreservesAllScalarFields(t *testing.T) {
	yes, no := true, false
	src := &Data{
		Model:          "claude-opus-4-7",
		Theme:          "dark",
		SearchProvider: "exa",
		AllowBypass:    &yes,
		AutoMemory:     &no,
		Persona:        "ml-researcher",
		SelfLearn: SelfLearnSettings{
			Skills:   SelfLearnSkills{DenyCreate: true},
			Strategy: "custom",
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
	if dst.AutoMemory == nil || *dst.AutoMemory != *src.AutoMemory {
		t.Errorf("AutoMemory: got %v, want %v", dst.AutoMemory, src.AutoMemory)
	}
	if dst.Persona != src.Persona {
		t.Errorf("Persona: got %q, want %q", dst.Persona, src.Persona)
	}
	// SelfLearn is value-typed; the whole struct (incl. nested Skills) must
	// survive. Skipping this row caused /config to silently
	// show stale defaults until the bug was caught.
	if dst.SelfLearn != src.SelfLearn {
		t.Errorf("SelfLearn: got %+v, want %+v", dst.SelfLearn, src.SelfLearn)
	}
}

// TestMergeSettingsPreservesSelfLearn guards the merger gap that left the
// entire L1 feature unreachable from settings.json: mergeSettings used to
// drop the SelfLearn field on every load and every save merge.
func TestMergeSettingsPreservesSelfLearn(t *testing.T) {
	base := &Data{
		SelfLearn: SelfLearnSettings{
			Skills: SelfLearnSkills{DenyUpdate: true},
		},
	}
	overlay := &Data{
		SelfLearn: SelfLearnSettings{
			Skills:   SelfLearnSkills{DenyCreate: true},
			Strategy: "overlay strategy",
		},
	}
	got := mergeSettings(base, overlay)

	// Skills field-merges: Deny* OR across levels (overlay's DenyCreate + base's
	// DenyUpdate both survive); the shared Strategy coalesces from the overlay.
	if !got.SelfLearn.Skills.DenyCreate || !got.SelfLearn.Skills.DenyUpdate || got.SelfLearn.Strategy != "overlay strategy" {
		t.Errorf("Skills: got %+v, want merged overlay", got.SelfLearn.Skills)
	}

	// Symmetric: a base-only field survives an overlay that doesn't mention it.
	baseOnly := &Data{SelfLearn: SelfLearnSettings{Skills: SelfLearnSkills{DenyDelete: true}}}
	emptyOverlay := &Data{}
	got = mergeSettings(baseOnly, emptyOverlay)
	if !got.SelfLearn.Skills.DenyDelete {
		t.Errorf("base-only SelfLearn must survive empty overlay; got %+v", got.SelfLearn)
	}
}
