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
// ActionPermissions: disallowed actions are stripped so the model doesn't
// waste turns proposing them. The hard floor remains the permission gate at
// dispatch — this is just steering.
//
// See notes/active/l1-background-review.md §3.
func buildReviewPrompt(kinds ReviewKind, cwd string, memory *MemoryStore, skills *SkillManager) string {
	var b strings.Builder

	b.WriteString(reviewPreamble)
	b.WriteString("\n\n")
	b.WriteString(reviewToolScope)

	if kinds.Has(KindMemory) {
		b.WriteString("\n\n")
		b.WriteString(memorySectionFor(memory))
		b.WriteString("\n\nCurrent memory store (MEMORY.md):\n")
		if mem, ok := system.LoadAutoMemory(cwd); ok {
			b.WriteString("```\n")
			b.WriteString(mem)
			b.WriteString("\n```")
		} else {
			b.WriteString("(empty — no entries yet)")
		}
	}

	if kinds.Has(KindSkills) {
		b.WriteString("\n\n")
		b.WriteString(skillSectionFor(skills))
		b.WriteString("\n\nExisting skills:\n")
		b.WriteString(renderInventory(skills))
	}

	b.WriteString("\n\n")
	b.WriteString(reviewClosing)
	return b.String()
}

// skillSectionFor returns the skill review-prompt section tailored to the
// permissions the SkillManager will enforce at dispatch. See §5.5: stripping
// disallowed actions from the prompt prevents the model from proposing things
// it can't do — the permission layer is still the hard floor.
func skillSectionFor(mgr *SkillManager) string {
	perms := DefaultActionPermissions()
	if mgr != nil {
		perms = mgr.Perms()
	}

	var b strings.Builder
	b.WriteString("SKILLS (skill_manage tool). A skill is a reusable, class-level technique (e.g. go-table-tests), not a session-specific note.")

	// Steer the preference order based on what's actually allowed.
	if perms.AllowCreate {
		b.WriteString(" Prefer the broadest reuse; create is the last resort.")
	} else {
		b.WriteString(" Creation is disabled: only modify existing skills.")
	}

	b.WriteString("\nDecide in this order (preference: UPDATE > DELETE > CREATE):\n")
	step := 1
	if perms.AllowUpdate {
		fmt.Fprintf(&b, "%d. UPDATE — patch an existing skill when ANY of the following:\n", step)
		b.WriteString("     · a skill loaded / consulted this turn was proven wrong, incomplete, or outdated;\n")
		b.WriteString("     · an existing umbrella skill covers the new learning (extend it; consider adding a references/templates/scripts support file);\n")
		b.WriteString("     · the user voiced a style / format / workflow correction that belongs in the skill governing that task (embed it so the next session starts already knowing).\n")
		step++
	}
	if perms.AllowDelete {
		fmt.Fprintf(&b, "%d. DELETE — retire an agent-created skill when ANY of the following:\n", step)
		b.WriteString("     · the new learning supersedes the entire skill wholesale (replace, don't coexist);\n")
		b.WriteString("     · the skill encoded a transient / environment-dependent failure that is now resolved (the skill is now wrong);\n")
		b.WriteString("     · the skill turned out to encode an anti-pattern.\n")
		step++
	}
	if perms.AllowCreate {
		fmt.Fprintf(&b, "%d. CREATE — only when ALL of the following hold:\n", step)
		b.WriteString("     · the turn produced a non-trivial, generalizable technique / fix / pattern;\n")
		b.WriteString("     · NO existing skill (agent OR user) covers this class of task;\n")
		b.WriteString("     · the name is class-level (e.g. go-table-tests), NOT a PR number, error string, codename, or 'fix-X / debug-Y / audit-Z-today' session artifact;\n")
		b.WriteString("     · the learning is not an anti-pattern (see below).\n")
		b.WriteString("     Pick the level: reusable / general → user; project-specific → project.\n")
		step++
	}

	// Scope rule.
	if perms.AllowUpdateUserCreated {
		b.WriteString("You may patch any existing skill (including user-created); creation and deletion remain restricted to agent-created skills.\n")
	} else {
		b.WriteString("You may only modify skills marked editable (agent-created); read user-created skills to avoid duplication but never change them.\n")
	}

	b.WriteString("ANTI-PATTERNS (do NOT capture as a skill): environment-dependent failures, negative claims about tools, transient errors that resolved on retry, one-off task narratives. If the only candidate falls in this bucket, save nothing.")
	return b.String()
}

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
// advertises Bash/Read/Edit/Write/Glob/Grep that the fork rejects via its
// permission policy. Without this clarifier the reviewer LLM happily emits
// e.g. `Read('./SKILL.md')` calls that burn turns getting rejected, and the
// 5-minute deadline expires before any real write lands.
const reviewToolScope = `Tool scope for this review: the ONLY tools available are memory_write and skill_manage. Disregard any other tool mentioned in the system prompt above (Bash, Read, Edit, Write, Glob, Grep, etc.) — those belong to the main coding agent, not to this review pass. Calls to them will be rejected and waste the review's wall-clock budget.`

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
