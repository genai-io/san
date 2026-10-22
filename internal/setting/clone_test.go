package setting

import (
	"reflect"
	"testing"
)

// TestCloneCopiesEveryField guards against Clone() drift that would silently
// revert a setting to its default at startup (it has dropped SelfLearn and
// bashPreviewLines before). The source sets every field of Data — a new field
// left out of it fails the test — and the clone must equal it without sharing
// any pointer, slice or map.
func TestCloneCopiesEveryField(t *testing.T) {
	yes, no, three := true, false, 3
	src := &Data{
		Permissions:             PermissionSettings{DefaultMode: "plan", Allow: []string{"Read"}, Deny: []string{"Bash(rm:*)"}, Ask: []string{"Write"}},
		Model:                   "claude-opus-4-7",
		SubagentModel:           "inherit",
		Hooks:                   map[string][]Hook{"PreToolUse": {{Matcher: "Bash", Hooks: []HookCmd{{Type: "command", Command: "true"}}}}},
		Env:                     map[string]string{"K": "v"},
		EnabledPlugins:          map[string]bool{"p": true},
		DisabledTools:           map[string]bool{"WebFetch": true},
		Theme:                   "dark",
		SearchProvider:          "exa",
		AllowBypass:             &yes,
		AutoUpdate:              &no,
		AutoMemory:              &no,
		AutoMemoryDirectory:     "~/notes",
		StreamFirstChunkTimeout: "5m",
		StreamIdleTimeout:       "2m",
		HookUITimeout:           "30s",
		ContextBar:              &yes,
		GitBar:                  &yes,
		ThinkingDisplay:         ThinkingDisplayCollapsed,
		BashPreviewLines:        &three,
		Persona:                 "ml-researcher",
		SelfLearn:               SelfLearnSettings{Skills: SelfLearnSkills{DenyCreate: true}, Strategy: "custom"},
		AutoPilot:               AutoPilotSettings{Model: "openai/gpt"},
		LastOperationMode:       "auto",
	}
	v := reflect.ValueOf(*src)
	for i := range v.NumField() {
		if v.Field(i).IsZero() {
			t.Fatalf("set Data.%s in this test's source so Clone is checked for it", v.Type().Field(i).Name)
		}
	}

	dst := src.Clone()
	if !reflect.DeepEqual(dst, src) {
		t.Fatalf("Clone differs from its source:\n got %+v\nwant %+v", dst, src)
	}
	*dst.BashPreviewLines = 9
	dst.Permissions.Allow[0] = "changed"
	dst.Env["K"] = "changed"
	dst.Hooks["PreToolUse"][0].Hooks[0].Command = "changed"
	if *src.BashPreviewLines != 3 || src.Permissions.Allow[0] != "Read" || src.Env["K"] != "v" || src.Hooks["PreToolUse"][0].Hooks[0].Command != "true" {
		t.Fatal("Clone shares storage with its source")
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
