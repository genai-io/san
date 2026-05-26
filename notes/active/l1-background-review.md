# L1 — Background Review (per-turn self-learning)

Design for Layer 1 of the self-learning loop in
[#46](https://github.com/genai-io/gen-code/issues/46). This is
[#52](https://github.com/genai-io/gen-code/issues/52).

**Decision** (after comparing against the hermes-agent reference,
`agent/background_review.py`): gen-code uses an **out-of-band** review that
**writes memory/skills directly** per turn — useful on its own the moment it
ships. A later, separate **L2 curator** keeps the collection healthy; its design
is deferred to its own issue (a short rationale is in §5).

This doc focuses on L1: comparison, trigger, what it writes, fork mechanics, and
phasing.

---

## 1. How three systems do self-evolving memory (comparison)

Two places the "write memory" decision can live: **in-band** (the main agent
writes mid-session, on its own judgment) vs **out-of-band** (a separate agent
reviews after a turn and writes).

| Axis | Hermes (`background_review.py`) | Claude Code (auto memory) | gen-code (decided) |
|---|---|---|---|
| Write decision | **Out-of-band** review fork | **In-band** main agent | **Out-of-band** (aligned with Hermes) |
| When | After a turn | During the session, on model judgment | After a turn |
| Writes directly? | **Yes** (fork has memory + skill tools) | Yes (main agent's own tool calls) | **Yes** |
| Trigger | turns (memory) + tool-iters (skill) | model judgment + explicit "remember this" | same two signals |
| Memory path | `~/.hermes/MEMORY.md` + `USER.md` — **global** (`$HERMES_HOME`, not per-project) | `~/.claude/projects/<repo>/memory/MEMORY.md` — **per-project**, `<repo>` = git-root path with `/`→`-`; index (first 200 lines/25KB) + topic files | `~/.gen/projects/<project>/memory/MEMORY.md` — **per-project**, like Claude Code |
| Project isolation | none (one global store) | yes (keyed on git repo; worktrees share) | yes (keyed on git repo) |

**gen-code already has the *injection* side** (from the reminder/compaction
work): memory loads + re-injects as `<system-reminder>` blocks
(`memory-user`/`memory-project`), refreshed from disk on PostCompact. What's
missing is the *write* side — `internal/reminder` injects but never persists.
L1 adds the write side.

**Why out-of-band + direct-write:** in-band (Claude Code) is cheapest but spends
main-context tokens every session and only appends in the flow of work.
Out-of-band keeps the main turn clean and lets a dedicated prompt review after
the reply is delivered. Direct write (vs staging proposals) makes Phase 1 useful
on its own and matches the production-proven hermes shape.

---

## 2. Trigger — two signals

| Review kind | Signal | Default | Rationale |
|---|---|---|---|
| Memory | user turns since last review | every 10 turns | User-modeling drifts on conversational cadence, not work intensity. |
| Skills | tool iterations within the turn | when this turn ≥ 10 tool iters | Skill capture should fire when the agent actually *did* work; tool-iter count is the cheap, provider-agnostic proxy (tokens are per-provider and post-hoc). |

- **Memory** fires on a **turn cadence** (default every 10 user turns) — it is
  not tied to whether work happened. When it fires, the reviewer reads the
  **recent conversation history** (the turn snapshot) and updates durable facts.
- **Skills** fire on **work done this turn** (tool-iters ≥ K), not on a turn
  cadence and not on "a skill was invoked". Whether a skill was invoked affects
  *which* skill to update (see §3b), not *whether* to review — otherwise new
  skills for tasks that had no skill yet would never be learned.

Both config-overridable; `0` disables that arm. Combined when both fire on the
same turn. The trigger is a **pure event consumer** subscribed to
`core.Agent.Outbox()`, reading `TurnEvent` (`Result.Turns`/`ToolUses`/
`StopReason`). No `internal/core` changes; counters live in the consumer and
hydrate from history on session resume.

---

## 3. What L1 writes — directly

The write tools (`memory_write` + `skill_manage`) belong **only to the L1
reviewer fork** in this phase. The main agent never writes memory/skills — it
only reads/invokes them (existing skills-directory injection + skill-view).
Because only the reviewer writes, provenance is simply **by location**: every
L1 write lands under agent-created paths, kept apart from user-authored skills,
so the future L2 curator can manage them without touching the user's. (Letting
the main agent write — Hermes-style provenance flags — can come later.)

### 3a. Memory flow

- Trigger: every N user turns (§2).
- The reviewer reads the recent conversation history and extracts **durable
  user/project facts** (preferences, project conventions, build/debug insights).
- Writes via `memory_write` to a **user-level, project-partitioned** store:
  `~/.gen/projects/<project>/memory/MEMORY.md` (+ topic files, Claude-Code-style:
  a concise `MEMORY.md` index, detail files loaded on demand). `<project>` is
  derived from the git repo so worktrees/subdirs of one repo share a store;
  fall back to the project root outside a repo.
- **Why user-level + project-partitioned, not in-repo**: it isolates memory per
  project but keeps it **machine-local and out of the repo**, so there is no
  commit/gitignore decision and no agent churn in git history. (This mirrors
  Claude Code's auto-memory.) Append/replace entries; "Nothing to save." valid.

**Injection integration (required).** `LoadMemoryFiles` currently reads only
user-authored files (`GEN.md`/`CLAUDE.md`/rules); it does **not** read this
store today. A small change must add `~/.gen/projects/<project>/memory/MEMORY.md`
as a **new, distinct memory source** (its own level, e.g. "auto"), kept separate
from the user-authored `GEN.md`/`CLAUDE.md` — so agent-written memory and
user-written instructions never mix. Load the `MEMORY.md` index (cap like Claude
Code: first ~200 lines / 25KB); topic files load on demand. Without this read
side, L1 writes would never be injected.

### 3b. Skill flow — creation / update

Trigger: this turn did real work (tool-iters ≥ K). The reviewer runs the skill
review prompt against the turn snapshot and follows a **preference order**
(broadest reuse first; create new only as a last resort):

1. **Patch a skill that was loaded/used this turn.** If the agent consulted a
   skill (a skill-view in the turn history) and it was wrong/outdated →
   `skill_manage(patch, name, old, new)` to fix it. *(This is where "a skill was
   invoked" matters — it picks the target.)*
2. **Patch an existing umbrella.** List + view existing broad, class-level
   skills; if one covers this learning, patch it (add a pitfall/step, broaden a
   trigger).
3. **Add a support file** to an umbrella: `skill_manage(write_file, name,
   "references|templates|scripts/…", content)`, plus a pointer line in SKILL.md.
4. **Create a new class-level umbrella** — only when nothing above fits.
   `skill_manage(create, name, content)`. The name must be **class-level**
   (e.g. `go-table-tests`), never session-specific (no PR numbers, error
   strings, `fix-x-today`).

"Umbrella" is a **convention, not a data-model marker** — the preference order
above is what keeps the collection broad instead of sprouting one narrow skill
per session. New skills are written to `.gen/skills/agent-created/<name>/`
(a `SKILL.md` with frontmatter `name`/`description` + optional
`references/`/`templates/`/`scripts/`), isolated from user-authored skills.

`skill_manage` actions: `create`, `edit` (full rewrite — rare), `patch`,
`write_file`, `remove_file`, `delete`. **`patch`** is targeted find-and-replace
with a fuzzy-match chain (exact → line-trimmed → whitespace/indent/escape/
unicode-normalized → block-anchor → context-similarity) and an **escape-drift
guard** (rejects matches where transport-added `\'`/`\"` backslashes don't exist
in the file, prompting a clean re-read). Requires a unique match unless
`replace_all`.

Three review prompts, picked by which triggers fired (memory-only / skill-only /
combined). The skill prompt is **active** (most working sessions produce ≥1
update) and embeds an anti-pattern list (no environment-dependent failures, no
negative tool claims, no transient errors, no one-off task narratives).

---

## 4. Fork mechanics + invariants

Fresh `core.Agent` (`core.NewAgent`) run with `ThinkAct` in a goroutine — **not**
`subagent.Executor` (which is built for named, user-facing subagents with
registry/hooks/session-persistence a silent reviewer must not carry).

Invariants (each one cost hermes a production bug):

1. **Run AFTER the user reply is delivered.** Gate on `Result.StopReason ==
   StopEndTurn` (skip cancelled/interrupted/max-turns).
2. **Inherit the parent's cached system prompt byte-for-byte** for prefix-cache
   parity (Anthropic/OpenRouter). Hermes measured ~26% cost reduction.
3. **Tool whitelist at dispatch.** Fork's `tools[]` matches the parent (cache-key
   parity) but a static permission func allows only `memory_write` +
   `skill_manage`.
4. **Static `tool.WithPermission` only** — never `agent.PermissionBridge` (would
   deadlock the TUI); auto-deny any approval.
5. **Best-effort.** Wrap in recover; review failure never affects the user turn.
6. **No session-scoped side effects** (no hooks, no session persistence).
7. **Suppress fork status.** Only a one-line `💾 Self-improvement review:
   <summary>` surfaces on the main outbox (`MessageEvent`, `From: "l1-review"`).
   Silent on "nothing to save".
8. **≤1 in-flight fork per session.** Drop new triggers while one runs (log, no
   queue).

Module map:

| Concern | Module |
|---|---|
| Trigger + fork | new `internal/selflearn/l1` (subscribes to `core.Agent.Outbox()`, owns counters) |
| Wire-up | `internal/agent/session.go::Task.Start` (start), `stopLocked` (tear down) |
| Fork | `core.NewAgent` directly, restricted `core.Tools` |
| System prompt | pass the parent's `system.System` verbatim |
| Writes | `memory_write` → `~/.gen/projects/<project>/memory/`; `skill_manage` → agent-created skills (location TBD, §7) |
| Injection read | extend `LoadMemoryFiles` to read the memory store as a new "auto" source (§3a) |

---

## 5. Why a later L2 curator (rationale only — design deferred)

L1 writes with a **local** view (one turn) and **frequently**. Over many turns
the collection drifts in ways no single L1 write can fix: duplicate/overlapping
entries, contradictions and stale facts, unbounded `MEMORY.md` growth past the
injection budget. A separate, idle-triggered **L2 curator** evaluates the whole
collection and dedups/prunes/consolidates/archives — the same role hermes'
`agent/curator.py` plays. Its evaluation basis (usage telemetry + collection
intrinsics) and trigger policy are out of scope here and tracked in a separate
L2 issue.

One L1-side implication worth noting now: L2's strongest signal will be **usage**
(which skill was used, when), which gen-code doesn't record today. A light usage
log can be added with L1 or just before L2 — flagged so it isn't forgotten.

---

## 6. Phasing + prerequisites

- **Phase 1 — L1 (this issue, #52).** Trigger + fork + direct memory/skill writes
  + the three review prompts.
  - Prereqs:
    - `skill_manage` tool with patch semantics.
    - A first-class memory writer to `~/.gen/projects/<project>/memory/MEMORY.md`.
    - **Injection read side**: extend `LoadMemoryFiles` to load that store as a
      new, distinct "auto" source (§3a) — it does not today, so without this L1
      writes are never injected.
- **Phase 2 — L2 curator (separate issue).** Deferred (see §5).

### Concrete next steps (Phase 1)

1. New package `internal/selflearn/l1`: `Reviewer` (counters + Outbox
   subscription + trigger), `forkAgent(parent, snapshot, mode)` (restricted
   `core.Agent`, runs `ThinkAct`, surfaces the one-line summary).
2. `memory_write` (store at `~/.gen/projects/<project>/memory/`) + `skill_manage`
   tools; extend `LoadMemoryFiles` to read the memory store.
3. Review prompt templates (memory / skill / combined), rewritten for gen-code
   terminology.
4. Wire-up in `Task.Start` / `stopLocked`; gate on `StopEndTurn`.
5. Concurrency cap ≤1; drop-and-log on overlap.
6. Tests: trigger cadence (turns / iters / combined), interrupted-turn skip,
   concurrency cap, restricted-toolset enforcement.

---

## 7. Open questions (L1)

- **Skill store location.** Memory is project-partitioned at user level
  (`~/.gen/projects/<project>/memory/`, settled). Skills are different — a skill
  like `go-table-tests` is **reusable across projects**, so agent-created skills
  may belong at user-global level (e.g. `~/.gen/skills/agent-created/`, as hermes
  does with `~/.hermes/skills/`) rather than per-project or in-repo. Decide:
  user-global vs project-partitioned vs in-repo, and keep them isolated from
  user-authored skills regardless.
- **Cache parity on non-Anthropic providers** — verify system-prompt inheritance
  helps (or at least doesn't hurt) across gen-code's providers.
- **Usage telemetry** — whether to land the minimal usage log in this phase (for
  the future L2) or defer it entirely.

---

## References

- hermes-agent L1: `agent/background_review.py` (fork, prompts, direct writes);
  triggers in `agent/conversation_loop.py` (memory ~`:387–394`, skill
  ~`:4046–4051`, guard `:4062`). L2 (for context): `agent/curator.py`
  (idle-triggered maintenance).
- Claude Code memory model: <https://code.claude.com/docs/en/memory> (in-band
  contrast).
- gen-code turn loop & outbox: `internal/core/agent_impl.go` (`Run`, `TurnEvent`,
  `ThinkAct`).
- gen-code injection side (built): `internal/reminder` providers, PostCompact
  re-emit.
- Session wire-up: `internal/agent/session.go` (`Task.Start`).
- Permission model: `internal/agent/permission.go` (`PermissionBridge` — avoid),
  `internal/tool/perm` (static funcs L1 uses).
- Parent issue: <https://github.com/genai-io/gen-code/issues/46>;
  L1: <https://github.com/genai-io/gen-code/issues/52>
