package selflearn

import "testing"

// TestResolveRejectsTraversalNames guards the path-traversal fix: every
// action except create reaches disk through resolve(), which must reject a
// name carrying a path separator or "..". Before the fix only Create
// validated the name, so a crafted name flowed straight into filepath.Join.
func TestResolveRejectsTraversalNames(t *testing.T) {
	mgr, _ := newTestSkillManager(t)
	for _, bad := range []string{"../escape", "has/slash", "..", "foo/../bar", `a\b`} {
		if _, err := mgr.Patch(bad, "x", "y", false); err == nil {
			t.Errorf("Patch(%q) should be rejected", bad)
		}
		if _, err := mgr.Delete(bad); err == nil {
			t.Errorf("Delete(%q) should be rejected", bad)
		}
		if _, err := mgr.WriteFile(bad, "references/x.md", "c"); err == nil {
			t.Errorf("WriteFile(%q) should be rejected", bad)
		}
	}
}

// TestCreateDescriptionRoundTrips guards the yamlScalar fix: a description
// that opens a YAML indicator (leading [, {, -, …) used to be written
// unquoted, producing frontmatter that parses as a flow node or fails
// outright — which then made every later parseSkill on that file error,
// leaving the skill permanently un-editable. The description must now
// round-trip through both parseSkill and Inventory.
func TestCreateDescriptionRoundTrips(t *testing.T) {
	mgr, _ := newTestSkillManager(t)
	cases := []struct{ name, desc string }{
		{"brackets", "[draft] release note"},
		{"braces", "{tip} use this"},
		{"dash", "- dash leading"},
		{"colon", "ratio a:b note"},
		{"hash", "count #42 fix"},
		{"plain", "plain text note"},
	}
	for _, tc := range cases {
		if _, err := mgr.Create(tc.name, tc.desc, "body", "user"); err != nil {
			t.Fatalf("Create(%q): %v", tc.desc, err)
		}
		if _, err := mgr.parseSkill(tc.name); err != nil {
			t.Fatalf("parseSkill after desc %q: %v (frontmatter is invalid YAML)", tc.desc, err)
		}
		var got string
		var found bool
		for _, info := range mgr.Inventory() {
			if info.Name == tc.name {
				found, got = true, info.Description
			}
		}
		if !found {
			t.Fatalf("skill %q absent from inventory", tc.name)
		}
		if got != tc.desc {
			t.Errorf("description round-trip for %q: got %q", tc.name, got)
		}
	}
}

// TestApplyPatchFuzzyNoOverlap guards the overlapping-window fix: a
// self-overlapping multi-line pattern that matches only under normalization
// must collect non-overlapping windows (mirroring the exact tier), instead
// of recording overlapping starts and then eating lines on the backward
// replace.
func TestApplyPatchFuzzyNoOverlap(t *testing.T) {
	// Trailing spaces make the exact tier miss so the TrimSpace tier runs;
	// "A\nA" self-overlaps across the three lines. Pre-fix this collected
	// starts {0,1} and corrupted the body down to "X"; post-fix it collects
	// {0} and leaves the trailing line intact.
	body := "A \nA \nA "
	out, err := applyPatch(body, "A\nA", "X", true)
	if err != nil {
		t.Fatalf("replace_all fuzzy: %v", err)
	}
	if out != "X\nA " {
		t.Fatalf("overlap corruption: got %q, want %q", out, "X\nA ")
	}
}
