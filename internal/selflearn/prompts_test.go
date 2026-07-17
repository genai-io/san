package selflearn

import (
	"strings"
	"testing"
)

// TestSkillSectionIsTriggerAware confirms the review prompt is scoped to the
// actions the trigger allowed this pass: a create pass (no skill used) frames
// capturing novel work; an update/delete pass (a skill was used) frames
// refining/retiring the used skill, and never offers create.
func TestSkillSectionIsTriggerAware(t *testing.T) {
	t.Run("create pass — no skill was used", func(t *testing.T) {
		s := skillSectionFor(SkillPermissions{AllowCreate: true})
		mustContain(t, s, "CASE B — no skill ran")
		mustContain(t, s, "CREATE one skill only if ALL hold")
		mustNotContain(t, s, "CASE A") // skill-use framing must not appear
	})

	t.Run("update+delete pass — one integrated keep/update/delete decision", func(t *testing.T) {
		s := skillSectionFor(SkillPermissions{AllowUpdate: true, AllowDelete: true})
		mustContain(t, s, "CASE A — a skill ran this turn")
		mustContain(t, s, "Pick exactly ONE")
		mustContain(t, s, "Never delete a skill that still helps")
		mustNotContain(t, s, "CREATE one skill")
	})

	t.Run("delete-only pass — pure value assessment", func(t *testing.T) {
		s := skillSectionFor(SkillPermissions{AllowDelete: true})
		mustContain(t, s, "DELETE it only if it no longer helps")
		mustContain(t, s, "KEEP it (save nothing)")
		mustNotContain(t, s, "UPDATE") // the update tool isn't offered this pass
	})

	t.Run("update-only pass — refine if valuable", func(t *testing.T) {
		s := skillSectionFor(SkillPermissions{AllowUpdate: true})
		mustContain(t, s, "UPDATE it if this turn showed")
		mustNotContain(t, s, "DELETE it only if")
	})
}

func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("expected substring %q in prompt, got:\n%s", needle, haystack)
	}
}

func mustNotContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Fatalf("unexpected substring %q in prompt, got:\n%s", needle, haystack)
	}
}
