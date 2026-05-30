package selflearn

import (
	"strings"
	"testing"
)

// TestSkillSectionAdaptsToPermissions confirms the §5.5 "prompt synthesis"
// rule: actions the SkillManager will veto at dispatch are stripped from the
// review prompt so the model doesn't propose them.
func TestSkillSectionAdaptsToPermissions(t *testing.T) {
	t.Run("default — all actions present, conservative scope", func(t *testing.T) {
		mgr, _ := newTestSkillManagerWithPerms(t, DefaultActionPermissions())
		s := skillSectionFor(mgr)
		mustContain(t, s, "patch that skill")
		mustContain(t, s, "delete it")
		mustContain(t, s, "create a new class-level skill")
		mustContain(t, s, "only modify skills marked editable (agent-created)")
		mustNotContain(t, s, "Creation is disabled")
	})

	t.Run("no create — last-resort line replaced by hard restriction", func(t *testing.T) {
		perms := DefaultActionPermissions()
		perms.AllowCreate = false
		mgr, _ := newTestSkillManagerWithPerms(t, perms)
		s := skillSectionFor(mgr)
		mustContain(t, s, "Creation is disabled")
		mustNotContain(t, s, "create a new class-level skill")
	})

	t.Run("no update — patch/extend steps removed", func(t *testing.T) {
		perms := ActionPermissions{AllowDelete: true} // create/update both off; delete only
		mgr, _ := newTestSkillManagerWithPerms(t, perms)
		s := skillSectionFor(mgr)
		mustNotContain(t, s, "patch that skill")
		mustNotContain(t, s, "An existing skill covers this learning → patch it")
		mustContain(t, s, "delete it")
	})

	t.Run("no delete — retire step removed", func(t *testing.T) {
		perms := DefaultActionPermissions()
		perms.AllowDelete = false
		mgr, _ := newTestSkillManagerWithPerms(t, perms)
		s := skillSectionFor(mgr)
		mustContain(t, s, "patch that skill")
		mustContain(t, s, "create a new class-level skill")
		mustNotContain(t, s, "delete it")
	})

	t.Run("advanced opt-in — scope rule widens for patch", func(t *testing.T) {
		perms := DefaultActionPermissions()
		perms.AllowUpdateUserCreated = true
		mgr, _ := newTestSkillManagerWithPerms(t, perms)
		s := skillSectionFor(mgr)
		mustContain(t, s, "patch any existing skill (including user-created)")
		mustNotContain(t, s, "only modify skills marked editable")
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
