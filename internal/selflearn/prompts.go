package selflearn

import (
	"fmt"
	"strings"

	"github.com/genai-io/san/internal/core/system"
)

// buildReviewPrompt assembles the review instruction appended as the final user
// message of the fork. It is selected by which arms fired (memory-only /
// skill-only / combined) and embeds the current memory and skill inventory so
// the fork refreshes/dedupes rather than blindly appending.
//
// Per §5.5 the skill section is synthesised against the SkillManager's
// permissions: disallowed actions are stripped so the model doesn't waste
// turns proposing them. The hard floor remains the permission gate at
// dispatch — this is just steering.
//
// See notes/active/l1-background-review.md §3.
func buildReviewPrompt(kinds ReviewKind, cwd string, memory *MemoryStore, skills *SkillManager, strategy string) string {
	var b strings.Builder

	b.WriteString(reviewPreamble)
	b.WriteString("\n\n")
	b.WriteString(reviewToolScope)

	// Guidance: the user's /evolve Strategy override replaces the built-in
	// per-arm sections wholesale; empty ⇒ the tailored built-in for whichever
	// arms fired.
	b.WriteString("\n\n")
	if strategy != "" {
		b.WriteString(strategy)
	} else {
		var sections []string
		if kinds.Has(KindMemory) {
			sections = append(sections, memorySectionFor(memory))
		}
		if kinds.Has(KindSkills) {
			perms := AllowAllSkillActions()
			if skills != nil {
				perms = skills.Perms()
			}
			sections = append(sections, skillSectionFor(perms))
		}
		b.WriteString(strings.Join(sections, "\n\n"))
	}

	// Dynamic context is always injected (it is state, not editable guidance).
	if kinds.Has(KindMemory) {
		b.WriteString("\n\nCurrent memory store (MEMORY.md):\n")
		// Read from the store's own dir so a configured memory path is honored;
		// fall back to the cwd default when no store was supplied.
		memDir := system.AutoMemoryDir(cwd)
		if memory != nil {
			memDir = memory.Dir()
		}
		if mem, ok := system.LoadAutoMemoryAt(memDir); ok {
			b.WriteString("```\n")
			b.WriteString(mem)
			b.WriteString("\n```")
		} else {
			b.WriteString("(empty — no entries yet)")
		}
	}
	if kinds.Has(KindSkills) {
		b.WriteString("\n\nExisting skills:\n")
		b.WriteString(renderInventory(skills))
	}

	b.WriteString("\n\n")
	b.WriteString(reviewClosing)
	return b.String()
}

// DefaultStrategy returns the built-in learning strategy the /evolve Strategy
// editor seeds with: the general memory guidance plus the general skill
// guidance (both the used-a-skill and the new-work cases), so a saved override
// applies to every pass. The live built-in (memorySectionFor / skillSectionFor
// above) is instead tailored per pass to the arms that fired.
func DefaultStrategy() string {
	return memorySectionFor(nil) + "\n\n" +
		skillPreamble + "\n\n" +
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

const skillPreamble = `SKILLS — the skill_manage tool writes reusable, class-level techniques (e.g. go-table-tests), never session-specific notes.
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

// reviewPreamble frames the fork as an out-of-band reviewer. The recap shown
// to the user is built from the action log of the actual tool calls (not from
// the model's own narration), so the closing instruction is just a sentinel
// — empty when nothing was saved — that lets the wire-up suppress the
// notification entirely.
const reviewPreamble = `You are the self-learning reviewer for a coding agent. The conversation above is a just-completed turn. Reflect on it and capture only durable learnings using the write tools available to you. You are out-of-band: your writes affect future sessions, not the one above. Be conservative — "nothing to save" is a perfectly good outcome. Do not narrate to the user; do the work via tool calls.`

// reviewToolScope reins in the inherited system prompt: the fork keeps the
// parent's system prompt verbatim for prefix-cache parity, but the parent
// advertises Bash/Read/Edit/Write that the fork rejects via its
// permission policy. Without this clarifier the reviewer LLM happily emits
// e.g. `Read('./SKILL.md')` calls that burn turns getting rejected, and the
// 5-minute deadline expires before any real write lands.
const reviewToolScope = `Tool scope for this review: the ONLY tools available are memory_write and skill_manage. Disregard any other tool mentioned in the system prompt above (Bash, Read, Edit, Write, etc.) — those belong to the main coding agent, not to this review pass. Calls to them will be rejected and waste the review's wall-clock budget.`

// memorySectionFor returns the eviction-first memory steering with the
// store's actual cap interpolated — the model needs to know the real
// budget when the user has lowered memory.maxKB below the default.
func memorySectionFor(mem *MemoryStore) string {
	cap := 25
	if mem != nil {
		cap = mem.MaxKB()
	}
	return fmt.Sprintf(`MEMORY (memory_write tool). Save durable facts that will matter in future sessions: user preferences and corrections, project conventions, environment/build/debug insights.

Eviction is part of the job, not an afterthought:

1. First, scan the current store below and retire stale / superseded / merged-PR-specific entries via action=remove. A pass that only adds is a missed pruning opportunity.
2. If an existing entry covers the same topic as your new learning, use action=replace to refresh it — never add a near-duplicate.
3. The store has a hard %d KB cap per file. When the index is near cap, you MUST prune another entry first before your new add will fit.
4. Only then, action=add for the genuinely new durable fact.

Do NOT save: one-off task state, transient errors, or "what we did this session" narratives — those are not durable.`, cap)
}

// reviewClosing tells the model (a) to set a "note" parameter on every
// memory_write / skill_manage call describing what THAT call changed —
// the per-action recap rows show "<kind · target>: <note>" — and (b)
// to close with one short overall summary line that the status bar
// surfaces as "✓ <summary>". "Nothing to save." suppresses the
// user-visible recap entirely (§6 invariant #7).
const reviewClosing = `Per-call note (REQUIRED): every memory_write / skill_manage call MUST include a "note" parameter — one short clause (≤80 chars) describing what THAT specific call changed. Examples:
  - memory_write: "added 3 race-condition repro tips", "removed vague tooling guidance", "replaced outdated build-cache note"
  - skill_manage: "trimmed examples section by 1.8KB", "added type-hint cheatsheet to references/", "removed the generic intro paragraph"
The note appears verbatim in the per-action recap row, so be concrete.

Closing line: after the tool calls (or after deciding none are warranted), reply with ONE short line:
  - If no writes occurred, reply with the literal string "Nothing to save."
  - Otherwise reply with a single sentence of at most 60 characters summarising the whole pass — the key target + the gist of the edit. Examples: "trimmed go-testing SKILL.md by 1.8KB", "saved debugging notes (3 entries)", "created python-typing skill". No bullet list, no quotes, no period — just the line.

The closing line is shown verbatim in the status bar; the per-call notes are shown in the recap. Keep both concrete and brief.`
