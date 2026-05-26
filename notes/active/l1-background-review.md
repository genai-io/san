# Self-learning loop — design (L1 reflect, L2 curate)

Design for the self-learning loop proposed in
[#46](https://github.com/genai-io/gen-code/issues/46). [#52](https://github.com/genai-io/gen-code/issues/52)
is Layer 1 (L1).

**Decision after design discussion:** gen-code uses an **out-of-band** write
model split into two stages — L1 *reflects and stages*, L2 *curates and
commits*. L1 never writes memory/skills directly.

This doc covers: how three systems approach self-evolving memory (horizontal
comparison), the two-stage loop, what L1 reflects on, where reflections are
stored, the trigger, the fork mechanics + invariants, and phasing.

---

## 1. How three systems do self-evolving memory (horizontal comparison)

There are two fundamentally different places the "write memory" decision can
live:

- **In-band** — the *main* agent writes memory itself, mid-session, using its
  file tools, on its own judgment.
- **Out-of-band** — after a turn, a *separate* agent reviews the turn and
  writes.

| Axis | Hermes (`background_review.py`) | Claude Code (auto memory) | gen-code (decided) |
|---|---|---|---|
| Where the write decision lives | **Out-of-band**: forked review agent | **In-band**: the main agent | **Out-of-band**, two-stage |
| When | After a turn (forked goroutine/thread) | During the session, whenever the model judges it worthwhile | After a turn (L1 fork) → periodically (L2 curator) |
| Trigger | Cadence: user-turns (memory) + tool-iters (skills) | Model judgment + explicit "remember this" | Cadence (same two signals) for L1; threshold for L2 |
| Who decides what to keep | A review prompt in the fork | The main agent with full live context | L1 fork proposes; **L2 decides what actually commits** |
| Writes directly to memory/skills? | **Yes** (fork has the write tools) | Yes (main agent's own tool calls) | **No at L1** — L1 stages; L2 commits |
| Extra model calls | Yes (one per review) | No (it's the main agent) | Yes (L1 fork + L2 curator) |
| Storage | `MEMORY.md` + `USER.md` | `MEMORY.md` (index, first 200 lines/25KB loaded each session) + topic files (on demand) | injection already built (`GEN.md`/`CLAUDE.md` via reminder providers); + `.gen/reflections/` (L1 staging) and `.gen/memory/` (L2 output) |

**What gen-code already has** (built in the reminder/compaction work): the
*injection* side. Memory is loaded and re-injected as `<system-reminder>`
blocks (`memory-user`/`memory-project`), refreshed from disk on PostCompact.
What is missing is the *write/reflect* side — `internal/reminder` injects but
never persists.

**Why out-of-band + staged for gen-code** (first-principles, not "copy
Hermes"):

- In-band (Claude Code style) is cheapest and the decider has full context, but
  it spends main-context tokens on a memory tool + MEMORY.md index every
  session and relies on the model proactively self-writing. It also can only
  *append* in the flow of work — it has no natural moment to step back and ask
  "is the existing memory redundant / too big / stale?"
- Out-of-band keeps the main turn clean and lets a dedicated prompt do a
  thorough review. The cost (an extra model call) is acceptable because it runs
  after the reply is delivered.
- **Splitting reflect (L1) from commit (L2)** is the key refinement over
  Hermes' direct-write fork. A single turn is a *local* view; durable
  memory/skill edits should be made with a *global* view (the current memory in
  full + multiple reflections). One observation may be noise; a pattern across
  reflections is signal. Staging also keeps L1 low-risk: it only appends to a
  journal and never corrupts the live memory, giving a clean audit trail. This
  maps onto #46's existing L1/L2 layering.

---

## 2. The two-stage loop

```
┌──────── main agent.Run loop ────────┐
│ ThinkAct → Result → emit TurnEvent  │   user gets the reply here
└───────────────┬─────────────────────┘
                │ TurnEvent on Outbox
                ▼
        L1 Reviewer (event consumer)
        ├── counters (turns_since_memory, iters_since_skill)
        ├── trigger check (Result.ToolUses, StopReason)
        └── on fire → fork a fresh core.Agent
                          │  reflect on the turn snapshot
                          ▼
              append ONE structured reflection record
              to .gen/reflections/         ← stages, does NOT touch memory/skills

        ... (later, on threshold / schedule) ...

        L2 Curator
        ├── reads accumulated reflections + current memory + current skills
        ├── consolidates / dedupes / prunes / resolves contradictions
        └── commits memory_write + skill_manage   ← the only writer of real memory/skills
```

- **L1 — reflect + stage** (this issue, #52). Per-turn, out-of-band fork.
  Reflects on the just-finished turn and appends one reflection record. Never
  writes memory/skills. Never blocks the user reply. Never busts the main
  conversation's prefix cache.
- **L2 — curate + commit** (next, separate issue). Periodic. Reads the staged
  reflections together with the *current* memory/skill state and commits the
  actual updates, with the global context needed to consolidate rather than
  blindly append.

---

## 3. What L1 reflects on (targets)

Three categories — not just skills/memory:

1. **Memory candidates** — durable user/project facts: who the user is,
   preferences, project conventions, build/debug insights.
2. **Skill candidates** — "how to do this class of task": a procedure/workflow
   update to an existing skill, or a new skill.
3. **Self-assessment + memory/skill health (meta)** — the agent's
   correctness / efficiency / effectiveness this turn (did it do well? wasted
   steps? mistakes worth a durable lesson?), **and** observations about the
   *existing* memory/skills (stale? redundant? `MEMORY.md` getting too big?).

Category 3 is what makes this a *reflection* loop and not just an append loop:
without it, L2 could only grow memory, never evaluate or prune it. It is the
concrete form of "reflect on the current memory scale + the agent's
correctness/efficiency/effectiveness."

---

## 4. Reflection record (L1 staging output)

- **Location:** `.gen/reflections/` (append-only; one record per L1 fire).
  Distinct from `.gen/memory/` (L2 output) and from user-curated skills.
- **Format:** one file per record (e.g. `<timestamp>-<turn>.md`) with YAML
  frontmatter + markdown body, so it is both machine-readable (L2) and
  human-auditable.
- **Suggested fields:**
  - `turn_id`, `session_id`, `time`, `trigger` (memory | skill | combined)
  - `memory_candidates`: proposed durable facts (each with a short rationale)
  - `skill_candidates`: proposed skill updates/new skills (target skill, change)
  - `self_assessment`: correctness / efficiency / effectiveness notes
  - `memory_health`: observations on existing memory/skills (stale, redundant,
    oversized) — input for L2 pruning
- L2 consumes and may delete/compact processed records (open question on
  retention).

---

## 5. Trigger — two signals (unchanged)

L1 fires on two independent signals rather than one counter:

| Review kind | Signal | Default | Rationale |
|---|---|---|---|
| Memory | user turns since last review | every 10 turns | User-modeling drifts on conversational cadence, not work intensity. |
| Skills | tool iterations within the turn | when this turn ≥ 10 tool iters | Skill capture should fire when the agent actually *did* work; tool-iter count is the cheap, provider-agnostic proxy (tokens are per-provider and post-hoc). |

Both intervals are config-overridable; `0` disables that arm. Combined when
both fire on the same turn.

The trigger is a **pure event consumer** subscribed to `core.Agent.Outbox()`,
reading `TurnEvent` (which already carries `Result.Turns`, `Result.ToolUses`,
`Result.StopReason`). No `internal/core` changes needed; counters live in the
consumer.

---

## 6. Fork mechanics + invariants

The fork is a freshly-constructed `core.Agent` (`core.NewAgent`), **not**
`subagent.Executor` (which is built for named, user-facing subagents with
registry/hooks/session-persistence that a silent reviewer must not carry).

Key change from the original draft: the L1 fork's only write tool is a
**`reflection_write`** that appends to `.gen/reflections/`. It does **not** get
`memory_write` / `skill_manage` — those belong to L2.

Invariants (each one cost Hermes a production bug; we copy them):

1. **Run AFTER the user reply is delivered.** Never compete with the main turn.
2. **Inherit the parent's cached system prompt byte-for-byte** for prefix-cache
   parity (Anthropic / OpenRouter). Hermes measured ~26% cost reduction.
3. **Tool whitelist at dispatch time.** Fork's `tools[]` matches the parent (so
   the cache key matches) but a static permission func denies everything except
   `reflection_write`.
4. **Auto-deny approval prompts** on the worker — use `tool.WithPermission`
   (static), never `agent.PermissionBridge` (would deadlock against the TUI).
5. **Best-effort only.** Wrap the spawn in recover; review failure must never
   affect the user's session.
6. **No session-scoped side effects** from the fork (no hooks, no session
   persistence, no external memory plugins).
7. **Suppress all status output.** Only a one-line summary surfaces:
   `💾 Self-improvement review: <summary>` on the main outbox
   (`MessageEvent`, `From: "l1-review"`). Silent on "nothing to record."
8. **Hydrate counters from history on resume**, so cadence isn't reset by a
   process restart.

Module map:

| Concern | Module |
|---|---|
| L1 trigger + fork | new `internal/selflearn/l1` (subscribes to `core.Agent.Outbox()`, owns counters, spawns forks) |
| Wire-up | `internal/agent/session.go::Task.Start` (start consumer), `stopLocked` (tear down) |
| Fork construction | `core.NewAgent` directly, restricted `core.Tools` |
| System prompt inheritance | pass the parent's `system.System` instance verbatim |
| Reflection write tool | new `reflection_write` → `.gen/reflections/` |
| L2 curator (next phase) | new `internal/selflearn/l2`; `memory_write` + `skill_manage` |

---

## 7. Phasing

- **Phase 1 — L1 reflect + stage (this issue, #52).** Trigger + fork +
  `reflection_write` + reflection-record schema. Output: records in
  `.gen/reflections/`. No live memory/skill writes yet.
  - Prereq: a minimal reflection store (`.gen/reflections/` writer). This is
    much lighter than the old prereqs because L1 no longer needs `memory_write`
    or `skill_manage`.
- **Phase 2 — L2 curate + commit (next, new issue).** Reads accumulated
  reflections + current memory/skills; consolidates/prunes; commits via
  `memory_write` + `skill_manage`.
  - Prereqs (moved here from L1): `skill_manage` tool with patch semantics; a
    first-class memory writer (`.gen/memory/MEMORY.md` + `USER.md`). gen-code's
    injection side already reads these once they exist.

This revises #52's original acceptance criteria, which had the L1 fork calling
`memory_write` + `skill_manage` directly. Under the staged model, #52 = "reflect
+ stage"; the real writes move to the L2 issue.

### Concrete next steps (Phase 1)

1. New package `internal/selflearn/l1`: `Reviewer` (counters + Outbox
   subscription + trigger), `forkAgent(parent, snapshot, mode)` (restricted
   `core.Agent`, runs `ThinkAct`, surfaces the one-line summary).
2. `reflection_write` tool + `.gen/reflections/` writer with the record schema
   from §4.
3. Reflection prompt templates (memory / skill / combined), rewritten for
   gen-code terminology. Each must emit a structured reflection record, and
   "nothing to record" is a valid output.
4. Wire-up in `Task.Start` / `stopLocked`; gate on `Result.StopReason ==
   StopEndTurn` (skip cancelled/interrupted/max-turns).
5. Concurrency cap: ≤1 in-flight fork per session; drop new triggers while a
   prior fork runs (log a warning, no queueing).
6. Tests: trigger cadence (turns / iters / combined), interrupted-turn skip,
   concurrency cap, reflection-record shape.

---

## 8. Open questions

- **Reflection-record retention.** Does L2 delete processed records, or
  compact them into a processed-archive? Avoid unbounded `.gen/reflections/`.
- **L2 trigger.** Cadence (every N reflections), schedule, or on-demand
  (`/curate`)? Out of scope for #52 but needs an answer before Phase 2.
- **On-disk skill layout.** `.gen/skills/agent-created/` vs mixed with user
  skills (so L2 lifecycle ops don't surprise the user).
- **Cache parity on non-Anthropic providers.** Verify system-prompt
  inheritance helps (or at least doesn't hurt) across gen-code's providers.
- **Memory-health signal quality.** How reliably can a single-turn fork judge
  "existing memory is redundant/stale" without loading the full memory? May
  need to give the L1 fork read access to `MEMORY.md` (read-only) for category-3
  reflection.

---

## References

- Hermes-agent L1 reference: `agent/background_review.py`; triggers in
  `agent/conversation_loop.py:432` (memory) and `:4191` (skills).
- Claude Code memory model: <https://code.claude.com/docs/en/memory>
  (CLAUDE.md vs auto memory; in-band agent-written memory).
- gen-code turn loop & outbox: `internal/core/agent_impl.go` (`Run`,
  `TurnEvent` emission, `ThinkAct`).
- gen-code injection side (already built): `internal/reminder` providers
  (`memory-user`/`memory-project`), PostCompact re-emit.
- Session wire-up point: `internal/agent/session.go` (`Task.Start`).
- Permission model: `internal/agent/permission.go` (`PermissionBridge` — what
  L1 must avoid), `internal/tool/perm` (static permission funcs L1 uses).
- Parent issue: <https://github.com/genai-io/gen-code/issues/46>;
  L1: <https://github.com/genai-io/gen-code/issues/52>
