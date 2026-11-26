package skill

import (
	"fmt"
	"strings"
)

// DefaultStrategy provides the default text for the Auto Skill strategy editor.
// editor seeds with: both the used-a-skill and the new-work cases, so a saved
// override applies to every pass. The live built-in (skillSectionFor) is
// instead tailored per pass.
func DefaultStrategy() string {
	return skillPreamble + "\n\n" +
		skillCaseUsedHeader + "\n" + skillEvaluateGuidance + "\n\n" +
		skillCaseNewHeader + "\n" + skillCreateGuidance + "\n\n" +
		skillWriteScope + "\n\n" + skillAntiPatterns
}

// skillSectionFor returns the skill review-prompt section tailored to the
// actions the trigger scoped for this pass: a create pass (no skill was used)
// frames capturing a technique from new work; a skill-use pass frames a single
// integrated decision about the used skill — keep, refine, or retire it. The
// SkillManager's permission gate remains the hard floor.
func skillSectionFor(perms SkillPermissions) string {
	var b strings.Builder
	b.WriteString(skillPreamble)
	b.WriteString("\n\n")
	if perms.AllowCreate {
		b.WriteString(skillCaseNewHeader)
		b.WriteString("\n")
		b.WriteString(skillCreateGuidance)
	} else {
		// A skill was used: whatever actions are allowed, this is ONE decision
		// about that skill, never several passes.
		b.WriteString(skillCaseUsedHeader)
		b.WriteString("\n")
		switch {
		case perms.AllowUpdate && perms.AllowDelete:
			b.WriteString(skillEvaluateGuidance)
		case perms.AllowUpdate:
			b.WriteString(skillUpdateGuidance)
		case perms.AllowDelete:
			b.WriteString(skillDeleteGuidance)
		}
	}
	b.WriteString("\n\n")
	b.WriteString(skillWriteScope)
	b.WriteString("\n\n")
	b.WriteString(skillAntiPatterns)
	return b.String()
}

// The skill section is built from first principles: one definition, then the
// two mutually-exclusive cases (a skill ran this turn vs. none did), then the
// hard exclusions. Each case names its trigger, then its decision.

const skillPreamble = `SKILLS — capture reusable, class-level techniques (e.g. go-table-tests), never session-specific notes.
Default to changing nothing. Only act on a durable, general learning from THIS turn.`

const skillCaseUsedHeader = `CASE A — a skill ran this turn (find it in the Skill tool calls above). Judge that one skill:`

const skillCaseNewHeader = `CASE B — no skill ran, but the turn did non-trivial new work worth reusing:`

// skillEvaluateGuidance frames the both-allowed case: one integrated judgement
// of the used skill's worth, choosing at most one of keep / update / delete.
const skillEvaluateGuidance = `  Pick exactly ONE (improving and retiring are one judgement, not two):
    KEEP    correct and still useful. The default — prefer it.
    UPDATE  still useful, but this turn showed it wrong/incomplete/outdated, or the user corrected a style/format/workflow it should carry. Patch the fix in.
    DELETE  no longer useful: obsolete, superseded, or an anti-pattern. Never delete a skill that still helps.`

// skillUpdateGuidance is the update-only case (delete disallowed).
const skillUpdateGuidance = `  UPDATE it if this turn showed it wrong/incomplete/outdated, or the user corrected a style/format/workflow it should carry — patch the fix in. Otherwise KEEP it (save nothing).`

// skillDeleteGuidance is the delete-only case (update disallowed): a pure worth
// check of the used skill.
const skillDeleteGuidance = `  DELETE it only if it no longer helps: obsolete, superseded, wrong, or an anti-pattern. Otherwise KEEP it (save nothing).`

const skillCreateGuidance = `  CREATE one skill only if ALL hold:
    - a non-trivial, generalizable technique/fix/pattern
    - no existing skill (yours or the user's — see inventory) already covers it
    - a class-level name (go-table-tests), not a PR number, error string, codename, or fix-X/debug-Y
    - not an anti-pattern (below)
  Scope: general → user; project-specific → project.`

const skillWriteScope = `Write only your own (agent-created) skills; read the user's to avoid duplication, but never change them.`

const skillAntiPatterns = `NEVER a skill: environment-specific failures, "tool X is broken" claims, transient errors that passed on retry, one-off task narratives. If that's all you have, save nothing.`

func renderInventory(skills *SkillManager) string {
	if skills == nil {
		return "(none)"
	}
	inv := skills.Inventory()
	if len(inv) == 0 {
		return "(none)"
	}
	var b strings.Builder
	for _, s := range inv {
		edit := "read-only (user-created)"
		if s.Editable() {
			edit = "editable (agent-created)"
		}
		desc := s.Description
		if desc == "" {
			desc = "(no description)"
		}
		b.WriteString(fmt.Sprintf("- %s [%s, %s] — %s\n", s.Name, s.Level, edit, desc))
	}
	return strings.TrimRight(b.String(), "\n")
}
