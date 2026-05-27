package selflearn

import (
	"fmt"
	"strings"

	"github.com/genai-io/gen-code/internal/core/system"
)

// buildReviewPrompt assembles the review instruction appended as the final user
// message of the fork. It is selected by which arms fired (memory-only /
// skill-only / combined) and embeds the current memory and skill inventory so
// the fork refreshes/dedupes rather than blindly appending. See
// notes/active/l1-background-review.md §3.
func buildReviewPrompt(kinds ReviewKind, cwd string, skills *SkillManager) string {
	var b strings.Builder

	b.WriteString(reviewPreamble)

	if kinds.Has(KindMemory) {
		b.WriteString("\n\n")
		b.WriteString(memorySection)
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
		b.WriteString(skillSection)
		b.WriteString("\n\nExisting skills:\n")
		b.WriteString(renderInventory(skills))
	}

	b.WriteString("\n\n")
	b.WriteString(reviewClosing)
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
		if s.Editable {
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

const reviewPreamble = `You are the self-learning reviewer for a coding agent. The conversation above is a just-completed turn. Reflect on it and capture only durable learnings using the write tools available to you. You are out-of-band: your writes affect future sessions, not the one above. Be conservative — "nothing to save" is a perfectly good outcome. Do not narrate; make the tool calls, then reply with a single short line summarizing what you changed (or "Nothing to save").`

const memorySection = `MEMORY (memory_write tool). Save durable facts that will matter in future sessions: user preferences and corrections, project conventions, environment/build/debug insights. Before adding, check the current store below: if an entry already covers the topic, use action=replace to refresh it instead of adding a near-duplicate; use action=remove for anything now wrong.
Do NOT save: one-off task state, transient errors, or "what we did this session" narratives — those are not durable.`

const skillSection = `SKILLS (skill_manage tool). A skill is a reusable, class-level technique (e.g. go-table-tests), not a session-specific note. Prefer the broadest reuse; create is the last resort. Decide in this order:
1. A skill used this turn was wrong/outdated/incomplete → patch that skill.
2. An existing skill covers this learning → patch it (or add a references/templates/scripts support file).
3. A genuinely new class of task that no skill covers → create a new class-level skill (kebab name, no PR numbers or error strings). Pick the level: reusable/general → user, project-specific → project.
You may only modify skills marked editable (agent-created); read user-created skills to avoid duplication but never change them.
NOTHING TO SAVE when: the session ran smoothly with no correction or new technique, or the only candidate is an anti-pattern — environment-dependent failures, negative claims about tools, transient errors, or one-off task narratives.`

const reviewClosing = `Make any warranted tool calls now, then end with one short summary line.`
