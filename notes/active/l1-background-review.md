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

Combined when both fire on the same turn. The trigger is a **pure event
consumer** subscribed to `core.Agent.Outbox()`, reading `TurnEvent`
(`Result.Turns`/`ToolUses`/`StopReason`). No `internal/core` changes; counters
live in the consumer and hydrate from history on session resume.

**Configuration.** The two arms are toggled and tuned independently via
`settings.json` (a new `selfLearn` section, merged across user/project/local
layers like other gen-code settings):

```json
{
  "selfLearn": {
    "memory": { "enabled": false, "everyTurns": 10 },
    "skills": { "enabled": false, "everyToolIters": 10 }
  }
}
```

- `memory.enabled` / `skills.enabled` — enable each arm separately. You can run
  memory-evolving without skill-evolving and vice versa.
- `memory.everyTurns` — memory cadence (user turns); `skills.everyToolIters` —
  skill threshold (tool iterations this turn).
- **When both arms are off, no L1 reviewer goroutine is started** — zero
  overhead, no extra model calls, nothing written.
- **Default: off (opt-in).** L1 forks an extra model call per cadence and writes
  files automatically; ship it opt-in, then consider defaulting on once trusted
  (Claude Code ships auto-memory on — we can match later). *(Default value is an
  easily-changed decision.)*
- Optional escape hatch: an env override (e.g. `GEN_DISABLE_SELF_LEARN=1`),
  mirroring Claude Code's `CLAUDE_CODE_DISABLE_AUTO_MEMORY`.

---

## 3. What L1 writes — directly

The write tools (`memory_write` + `skill_manage`) belong **only to the L1
reviewer fork** in this phase; the main agent reads/invokes skills and memory
but never writes them. L1-written content is marked **agent-owned** — skills via
the `origin: agent-created` frontmatter field (§3b), memory by living in its own
dedicated store (§3a) — so the future L2 curator manages only agent-owned
content and never the user's. (Letting the main agent write skills too can come
later.)

### 3a. Memory flow — when to add vs replace

**Precondition (whether a memory review runs):** the memory cadence is due
(every N user turns, §2), the memory arm is enabled, and the turn ended cleanly
(`StopEndTurn`). Unlike skills, this is a **turn cadence** — independent of how
much work the turn did.

**Inputs to the fork:** the conversation snapshot (the recent turns) + the
memory-review prompt; the fork also **reads the current memory store** so it can
refresh/dedupe rather than blindly append.

```
memory cadence due  (memory arm enabled)
  │  StopEndTurn ?   ── no ──▶ skip (cancelled / interrupted)
  ▼ yes
memory review fork  (inherits system prompt; reads conversation snapshot +
                     current MEMORY.md)
  │
  ▼
a durable fact worth keeping?  (user preference, project convention,
  │                             build/debug insight — NOT one-off task state)
  │  no ──▶ Nothing to save
  ▼ yes
already an entry about this?
  │  yes ──────────▶  memory_write(replace …)   refresh / correct it
  ▼ no
  └────────────────▶  memory_write(add …)       append new entry
                          → ~/.gen/projects/<project>/memory/MEMORY.md
```

`memory_write` actions: `add`, `replace`, `remove`, operating on the memory
store (the `MEMORY.md` index; long detail may spill into topic files like
`debugging.md`, loaded on demand). "Nothing to save." is valid.

**Anti-patterns (don't save):** one-off task state, transient errors,
"what we did this session" narratives — those are not durable across sessions.

**Store:** `~/.gen/projects/<project>/memory/MEMORY.md` (+ topic files,
Claude-Code-style: concise index, details on demand). `<project>` is the **git
repo root path, separators replaced** (the same encoding Claude Code uses, e.g.
`-Users-me-work-gen-code`), so worktrees/subdirs of one repo share a store; fall
back to the project root outside a repo.

**Why user-level + project-partitioned, not in-repo:** isolates memory per
project but keeps it **machine-local and out of the repo** — no commit/gitignore
decision, no agent churn in git history. (Mirrors Claude Code's auto-memory.)

**Injection integration (required).** `LoadMemoryFiles` currently reads only
user-authored files (`GEN.md`/`CLAUDE.md`/rules); it does **not** read this
store today. A small change must add `~/.gen/projects/<project>/memory/MEMORY.md`
as a **new, distinct memory source** (its own level, e.g. "auto"), kept separate
from the user-authored `GEN.md`/`CLAUDE.md` — so agent-written memory and
user-written instructions never mix. Without this read side, L1 writes would
never be injected.

**Load timing — reuses the existing injection lifecycle:**

- **Session start:** read the `MEMORY.md` index → cache → inject as a
  `<system-reminder source="memory-auto">` block on the first user message.
  Cap the index like Claude Code (first ~200 lines / 25KB); topic files are read
  on demand by the agent's file tools, not injected.
- **PostCompact:** re-read from disk (`refreshMemoryContext`) + re-emit
  (`RequeueSystemReminders`) — the same path already built for `GEN.md`/`CLAUDE.md`
  memory — so the latest memory survives compaction.
- **cwd change:** re-read, because `<project>` (and thus the store) changes when
  the working directory moves to a different repo.

**Update timing:** the L1 fork writes post-turn, on the memory cadence (every N
user turns, §2), gated on `StopEndTurn`.

**Write→visibility lag (by design):** L1 writes out-of-band, while the running
session's memory was injected at a load point. So a fresh write is **not**
live-patched into the in-flight context — it becomes visible at the next load
point: the next **PostCompact** (which re-reads from disk) or the next **session
start**. Acceptable, since memory mainly serves future turns/sessions.

### 3b. Skill flow — when to create vs update

> **Umbrella** = a broad, **class-level** skill (e.g. `go-testing`,
> `code-review`) that accumulates many specific learnings over time — as opposed
> to a **narrow, session-specific** skill (`fix-flaky-test-pr-1234`). The whole
> flow prefers extending an umbrella over spawning narrow skills, so the
> collection stays "broad and few", not "narrow and many".

**Precondition (whether a skill review runs at all):** this turn did real work
(tool-iters ≥ K, §2), the turn ended cleanly (`StopEndTurn`), and the skill arm
is enabled. The trigger is about *work done* — not about whether a skill was
invoked.

Once the review runs, the model chooses create / update / nothing by this
decision flow (broadest reuse first; create is the last resort):

```
turn ends
  │  tool-iters ≥ K ?                    ── no ──▶ no skill review
  │  StopEndTurn & skill arm enabled ?   ── no ──▶ skip
  ▼ yes
skill review fork  (reads the turn snapshot)
  │
  ▼
① a skill loaded/used this turn was wrong / outdated / incomplete ?
  │  yes ──────────▶  UPDATE · patch that skill
  ▼ no
② an existing umbrella skill covers this learning ?
  │  yes ──────────▶  UPDATE · patch the umbrella, or add a
  │                            references/templates/scripts support file
  ▼ no
③ a NEW, generalizable class of task that no skill covers ?
  │  yes ──────────▶  CREATE · new class-level umbrella (origin: agent-created)
  ▼ no
            Nothing to save   (smooth session, or only anti-patterns)
```

**UPDATE — when an applicable skill already exists (① or ②).** Preferred (keeps
the collection broad, avoids near-duplicates). `skill_manage(patch, …)` to
fix/extend an existing skill; `skill_manage(write_file, …)` to add a
`references/templates/scripts` support file (plus a pointer line in `SKILL.md`).
Patch the skill **in place**, at its existing scope.

**CREATE — only when the learning is a genuinely new class of task no skill
covers (③).** Last resort. `skill_manage(create, name, content)`; the name must
be **class-level** (e.g. `go-table-tests`), never session-specific (no PR
numbers, error strings, `fix-x-today`). Always written with
`origin: agent-created`, at the level L1 chose (user vs project, below).

**NOTHING TO SAVE — when** the session ran smoothly with no correction and no
new technique, or the only candidate is an **anti-pattern**: environment-
dependent failures, negative claims about tools, transient errors, one-off task
narratives.

"Umbrella" is a **convention, not a data-model marker** — the decision flow
above is what keeps the collection broad instead of sprouting one narrow skill
per session.

**Two levels (user + project), for both create and update.** gen-code already
loads skills from two scopes — `ScopeUser` (`~/.gen/skills/`) and `ScopeProject`
(`.gen/skills/`) — so no new loader source is needed (unlike memory). Skills
live directly in those dirs (`~/.gen/skills/<name>/`, `./.gen/skills/<name>/`);
**no separate `agent-created/` subdir.**

**Provenance is a frontmatter field, not a directory.** Add one field to
`SKILL.md`, e.g. `origin: agent-created`; **absent = `user-created`** (the
default). The `Skill` struct (`internal/skill/types.go`) currently has no
metadata map, so this is a one-field addition (`Origin string`, Claude ignores
unknown frontmatter). This mirrors hermes, which marks provenance with a flag,
not a separate path. (A sidecar file is the alternative; a field is simpler for
Phase 1.)

- **On create**, L1 writes `origin: agent-created` and picks the level:
  reusable/general → user (`~/.gen/skills/`); specific to this project →
  project (`./.gen/skills/`). The review prompt encodes the rule.
- **On update**, patch the skill **in place** (don't relocate or change scope).
- **Scope of L1 writes (Phase 1):** L1 only creates/patches skills it owns
  (`origin: agent-created`); it reads/consults `user-created` skills (to avoid
  duplication) but does **not** modify them. The future L2 curator likewise only
  manages `agent-created` skills, never the user's.

**Why this differs from memory:** memory is personal/accumulated → machine-local,
project-partitioned, not in the repo. Skills are reusable artifacts a team may
want to share → they live in gen-code's existing user/project scopes (project-
level in-repo `./.gen/skills/`), distinguished only by the `origin` field.

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

**Construction & seeding:** the fork inherits the parent's `system.System`
verbatim, is seeded with the parent's **message snapshot** (`SetMessages`) plus
an injected user message carrying the review prompt, then runs `ThinkAct` under
a **tight cap** — `MaxTurns ≈ 16` and a context deadline (≈5 min); drop the
result if it exceeds either.

Invariants (each one cost hermes a production bug):

1. **Run AFTER the user reply is delivered.** Gate on `Result.StopReason ==
   StopEndTurn` (skip cancelled/interrupted/max-turns).
2. **Inherit the parent's cached system prompt byte-for-byte** for prefix-cache
   parity (Anthropic/OpenRouter). Hermes measured ~26% cost reduction.
3. **Toolset whitelist at dispatch.** Fork's `tools[]` matches the parent
   (cache-key parity) but a static permission func allows only the **memory +
   skill toolsets** — both **read and write**: it must read current memory and
   list/view existing skills (to dedupe and choose add-vs-replace / patch-vs-
   create), and write via `memory_write` / `skill_manage`. Everything else
   denied.
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
| Writes | `memory_write` → `~/.gen/projects/<project>/memory/`; `skill_manage` → `~/.gen/skills/<name>/` (user) or `./.gen/skills/<name>/` (project), with `origin: agent-created` |
| Provenance | add `Origin` to the skill frontmatter struct (`internal/skill/types.go`); absent = `user-created` |
| Injection read | memory: extend `LoadMemoryFiles` with a new "auto" source (§3a). skills: no change — existing user/project scope loader already covers it. |

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
4. Add the `selfLearn` settings section (`internal/setting`); wire-up in
   `Task.Start` / `stopLocked` — start the reviewer only when ≥1 arm is enabled,
   pass the enabled arms + intervals to it; gate reviews on `StopEndTurn`.
5. Concurrency cap ≤1; drop-and-log on overlap.
6. Tests: trigger cadence (turns / iters / combined), interrupted-turn skip,
   concurrency cap, restricted-toolset enforcement.

---

## 7. Open questions (L1)

- **Let L1 patch user-authored skills?** Phase 1 default is no (L1 only writes
  `origin: agent-created` skills). Hermes allows patching a just-used user skill
  (its preference #1); revisit if "fix the skill I just used" turns out
  important.
- **Commit agent-created project skills?** They live in-repo at `./.gen/skills/`
  mixed with user skills (distinguished by `origin`). Team choice whether to
  commit auto-generated ones; can be filtered by the `origin` field if needed.
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
