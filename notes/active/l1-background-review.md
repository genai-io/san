# Self-learning loop — design (L1 capture, L2 curate)

Design for the self-learning loop in [#46](https://github.com/genai-io/gen-code/issues/46).
[#52](https://github.com/genai-io/gen-code/issues/52) is Layer 1 (L1).

**Decision** (after comparing against the hermes-agent reference implementation
at `agent/background_review.py` + `agent/curator.py`): gen-code uses an
**out-of-band, two-layer** loop, aligned with how hermes does it in production:

- **L1 — per-turn capture (direct write).** After a turn, fork a fresh
  `core.Agent` that reflects on the turn and writes memory/skill updates
  **directly**. Useful on its own, the moment it ships.
- **L2 — periodic curator (maintenance).** Runs on idle/schedule, not per turn.
  Evaluates the *collection's* health and dedups / prunes / consolidates /
  archives. Keeps the directly-written collection from drifting.

This doc covers: the horizontal comparison, why L1 direct-write still needs L2,
the L1 capture mechanics, the L2 evaluation basis, storage layout, and phasing.

---

## 1. How three systems do self-evolving memory (horizontal comparison)

Two fundamentally different places the "write memory" decision can live:

- **In-band** — the *main* agent writes memory itself, mid-session, on its own
  judgment, using its file tools.
- **Out-of-band** — after a turn, a *separate* agent reviews and writes.

| Axis | Hermes (`background_review.py` + `curator.py`) | Claude Code (auto memory) | gen-code (decided) |
|---|---|---|---|
| Write decision | **Out-of-band** review fork | **In-band** main agent | **Out-of-band**, aligned with Hermes |
| When | After a turn (L1), + idle (L2 curator) | During the session, on model judgment | After a turn (L1), + idle (L2) |
| L1 writes directly? | **Yes** — fork has memory + skill tools, persists immediately | Yes (main agent's own tool calls) | **Yes** (L1 captures directly) |
| L2 role | Curator: archive stale / consolidate / pin-unpin / audit | (none — in-band only) | Curator: evaluate + dedup/prune/consolidate |
| Trigger | turns (memory) + tool-iters (skill); curator on idle | model judgment + explicit "remember this" | same two signals; L2 on idle |
| Storage | `~/.hermes/MEMORY.md` + `USER.md`; `~/.hermes/skills/<name>/` | `MEMORY.md` (index, first 200 lines/25KB loaded) + topic files | `.gen/memory/` + `.gen/skills/agent-created/` |

**What gen-code already has** (from the reminder/compaction work): the
*injection* side — memory is loaded and re-injected as `<system-reminder>`
blocks (`memory-user`/`memory-project`), refreshed from disk on PostCompact.
What is missing is the *write* side (`internal/reminder` injects but never
persists) and the *curation* side.

**Why out-of-band + direct-write for gen-code** (first-principles):

- In-band (Claude Code) is cheapest and the decider has full context, but it
  spends main-context tokens every session and only appends in the flow of
  work — no natural moment to step back and prune. Out-of-band keeps the main
  turn clean and lets a dedicated prompt review thoroughly after the reply.
- **L1 writes directly** (rather than staging proposals for L2 to commit)
  because direct write makes Phase 1 useful on its own and matches the
  production-proven hermes shape. The churn risk of a local, frequent,
  append-only writer is handled by L2, not by deferring all writes.

---

## 2. Two layers: capture (L1) → curate (L2)

Different **scope**, not just different cadence:

- **L1 = capture.** Local view (this one turn), frequent, additive. Good at
  "spotting something new", blind to the collection as a whole.
- **L2 = groundskeeper.** Global view (the whole memory/skill collection),
  periodic, consolidating.

**Why direct-write L1 still needs L2.** Because L1 only ever sees one turn and
writes immediately, over many turns the collection *drifts* in ways no single
L1 write can fix:

1. **Duplication / overlap** — L1 re-discovers and re-writes near-duplicate
   entries/skills. L2 merges.
2. **Contradiction / staleness** — a preference changes; a skill goes out of
   date. L1 can't know "this conflicts with an older entry". L2 reconciles and
   archives.
3. **Unbounded growth** — direct appends bloat `MEMORY.md` past the injection
   budget (first 200 lines / 25KB). L2 trims/compacts so the injected index
   stays useful.
4. **Reorg / promotion** — a one-off note may deserve promotion to an umbrella
   skill; scattered notes get consolidated. L2 restructures.
5. **Lifecycle** — pin/unpin, archive long-unused items.

hermes uses `agent/curator.py` (idle-triggered, not per-turn) for exactly this.

---

## 3. L1 — per-turn capture (this issue, #52)

### Trigger — two signals

| Review kind | Signal | Default | Rationale |
|---|---|---|---|
| Memory | user turns since last review | every 10 turns | User-modeling drifts on conversational cadence, not work intensity. |
| Skills | tool iterations within the turn | when this turn ≥ 10 tool iters | Skill capture should fire when the agent actually *did* work; tool-iter count is the cheap, provider-agnostic proxy (tokens are per-provider and post-hoc). |

Both config-overridable; `0` disables that arm. Combined when both fire on the
same turn. The trigger is a **pure event consumer** subscribed to
`core.Agent.Outbox()`, reading `TurnEvent` (`Result.Turns/ToolUses/StopReason`).
No `internal/core` changes; counters live in the consumer and hydrate from
history on resume.

### What L1 writes — directly

- **Memory**: durable user/project facts → `.gen/memory/MEMORY.md` / `USER.md`.
- **Skill**: how to do a class of task → `.gen/skills/agent-created/<name>/`
  (create or patch an agent-created skill; never touch user-curated/pinned).

Three review prompts picked by which triggers fired (memory-only / skill-only /
combined). "Nothing to save" is valid; for skills it should not be the default.
Skill prompt embeds an anti-pattern list (no environment-dependent failures, no
negative tool claims, no transient errors, no one-off task narratives).

### Fork mechanics + invariants

Fresh `core.Agent` (`core.NewAgent`), **not** `subagent.Executor`. Invariants
(each cost hermes a production bug):

1. **Run AFTER the user reply is delivered**; gate on `Result.StopReason ==
   StopEndTurn` (skip cancelled/interrupted/max-turns).
2. **Inherit the parent's cached system prompt byte-for-byte** (prefix-cache
   parity; hermes measured ~26% cost reduction).
3. **Tool whitelist at dispatch**: fork's `tools[]` matches the parent (cache
   key parity) but a static permission func allows only `memory_write` +
   `skill_manage`.
4. **Static `tool.WithPermission` only** — never `agent.PermissionBridge`
   (would deadlock the TUI); auto-deny any approval.
5. **Best-effort**: wrap in recover; failure never affects the user turn.
6. **No session-scoped side effects** (no hooks, no session persistence).
7. **Suppress fork status**; only a one-line `💾 Self-improvement review:
   <summary>` surfaces on the main outbox. Silent on "nothing to save".
8. **≤1 in-flight fork per session**; drop new triggers while one runs (log,
   no queue).

### Emit usage telemetry (so L2 can evaluate later)

L2's strongest evaluation signal is *usage* (see §4), and gen-code does not
record it today. As part of normal operation (not the fork), record a light
usage log: per-skill last-used / invoke count, and memory-recall hits. This is
telemetry to *inform* L2 — distinct from the memory/skill writes themselves.
Minimal hooks: skill invocation (Skill tool / reminder injection) and memory
reference. Can land with L1 or just before L2.

---

## 4. L2 — periodic curator (next issue)

Evaluates the collection's health, then dedups / prunes / consolidates /
promotes / archives. Only touches agent-created / unprotected items.

### Evaluation basis

**(A) Usage / behavioral data (dynamic — the strongest signal):**
- skill activity: last-used time + frequency (archive long-idle; pin/unpin by
  activity).
- memory hit: was an entry ever recalled/relevant.
- correction signals: a user correction attributable to a memory/skill entry is
  strong evidence it is wrong → reconcile/remove.

Requires the usage telemetry from §3; without it L2 can only do static
evaluation.

**(B) Collection intrinsics (static — model reads and judges):**
- size vs injection budget (200 lines / 25KB) → trim/compact.
- redundancy/overlap → merge.
- contradiction → reconcile, keep newest.
- dangling references / stale facts → fix or archive.
- provenance/protection → what L2 is allowed to change.

### Maps to the original reflection dimensions

| Dimension | L2 basis |
|---|---|
| scale | size vs injection budget (static) |
| effective | usage: used? helped? (dynamic) |
| correct | contradiction + correction signals |
| efficient | redundancy/overlap (lean collection) |

### How it decides

Hybrid: **hard heuristics** (deterministic, cheap: "idle 30 days → archive
candidate", "over budget → must trim", "protected → never touch") +
**LLM judgment** (semantic: "do these two skills overlap?", "which contradicting
memory wins?", "promote to umbrella?"). L2 is a periodic agent run that reads
the whole collection + the usage log, produces an assessment, then executes.

### Trigger

Idle/periodic (e.g. idle ≥ N hours and ≥ M since last run), not per-turn.
Exact policy TBD (open question).

---

## 5. Storage layout

| What | Path | Writer |
|---|---|---|
| Agent memory | `.gen/memory/MEMORY.md` | L1 writes, L2 maintains |
| User profile | `.gen/memory/USER.md` | L1 writes, L2 maintains |
| Agent-created skills | `.gen/skills/agent-created/<name>/` | L1 writes, L2 maintains |
| Usage log | `.gen/usage/` (or similar) | normal operation; L2 reads |

Agent-created skills stay isolated from user-curated ones so L2 lifecycle ops
(archive/consolidate) never surprise the user. Injection already reads
`GEN.md`/`CLAUDE.md` via reminder providers; `.gen/memory/MEMORY.md` slots into
the same injection path.

---

## 6. Phasing + prerequisites

- **Phase 1 — L1 capture (this issue, #52).** Trigger + fork + direct
  memory/skill writes + the three review prompts. Useful on its own.
  - Prereqs: `skill_manage` tool with patch semantics; a first-class memory
    writer (`.gen/memory/MEMORY.md` + `USER.md`). (These move back to L1 since
    L1 now writes directly.)
- **Phase 2 — L2 curator (next, new issue).** Evaluation (usage + intrinsics) +
  dedup/prune/consolidate/archive.
  - Prereq: usage telemetry (§3) for activity-based evaluation.

### Concrete next steps (Phase 1)

1. New package `internal/selflearn/l1`: `Reviewer` (counters + Outbox
   subscription + trigger), `forkAgent(parent, snapshot, mode)` (restricted
   `core.Agent`, runs `ThinkAct`, surfaces the one-line summary).
2. `memory_write` + `skill_manage` tools (Phase-1 prereqs) and their stores
   under `.gen/`.
3. Review prompt templates (memory / skill / combined), rewritten for gen-code
   terminology.
4. Wire-up in `Task.Start` / `stopLocked`; gate on `StopEndTurn`.
5. Concurrency cap ≤1; drop-and-log on overlap.
6. Light usage telemetry hooks (so L2 has data when it ships).
7. Tests: trigger cadence (turns / iters / combined), interrupted-turn skip,
   concurrency cap, restricted-toolset enforcement.

---

## 7. Open questions

- **L2 trigger policy** (idle thresholds / schedule / on-demand `/curate`).
- **On-disk skill layout** confirmation (`.gen/skills/agent-created/`).
- **Usage-telemetry shape** — what exactly to log, where, retention.
- **Cache parity on non-Anthropic providers** — verify system-prompt
  inheritance helps (or doesn't hurt) across gen-code's providers.
- **Concurrency across writers** — L1 fork(s) vs a running L2 writing the same
  files; serialize or atomic-replace.

---

## References

- hermes-agent L1: `agent/background_review.py` (fork, prompts, direct writes);
  triggers in `agent/conversation_loop.py` (memory ~`:387–394`, skill
  ~`:4046–4051`, guard `:4062`); L2 curator: `agent/curator.py`
  (idle-triggered: archive stale / consolidate / pin-unpin / audit).
- Claude Code memory model: <https://code.claude.com/docs/en/memory>
  (CLAUDE.md vs in-band auto memory).
- gen-code turn loop & outbox: `internal/core/agent_impl.go` (`Run`,
  `TurnEvent`, `ThinkAct`).
- gen-code injection side (built): `internal/reminder` providers, PostCompact
  re-emit.
- Session wire-up: `internal/agent/session.go` (`Task.Start`).
- Permission model: `internal/agent/permission.go` (`PermissionBridge` — avoid),
  `internal/tool/perm` (static funcs L1 uses).
- Parent issue: <https://github.com/genai-io/gen-code/issues/46>;
  L1: <https://github.com/genai-io/gen-code/issues/52>
