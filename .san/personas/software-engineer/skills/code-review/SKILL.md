---
name: code-review
description: >-
  Review changed code for correctness bugs — logic errors, nil/error handling,
  concurrency, resource leaks, edge cases, broken invariants. Reports findings
  first, ordered by severity, then fixes on request. Scoped to correctness, not
  quality or style cleanups. Use when the user says "code review", "review my
  changes", "any bugs", "find bugs", or asks whether a change is correct.
allowed-tools:
  - Bash
  - Read
  - Glob
  - Grep
  - Edit
  - Agent
argument-hint: "[--fix] [focus area]"
---

# Code Review: Correctness

Review the changed code for **correctness bugs** — cases where it does the wrong
thing, crashes, corrupts state, or breaks a contract. This skill is deliberately
scoped to bugs. Do not report reuse, quality, style, or efficiency cleanups here;
those belong to a separate cleanup pass (San's `simplify` skill, where present).

If the arguments include `--fix`, apply fixes after reporting. Otherwise report
only and offer to fix. A leading focus area (e.g. `concurrency`) narrows the
review to that dimension.

## Phase 1: Identify Changes

Run `git diff` (or `git diff HEAD` when changes are staged) to see what changed.
If there are no git changes, review the most recently modified files the user
named or that you edited earlier in this conversation. Read enough of the
surrounding code that each changed line can be judged in context — a line is
rarely a bug on its own, only against the code that calls it and the code it
calls.

## Phase 2: Launch Review Agents in Parallel

Use the Agent tool to launch the dimension agents concurrently in a single
message (foreground — do NOT set `run_in_background`). Pass each agent the full
diff plus the context it needs. Each agent returns a list of findings; every
finding must carry:

- a **severity** — `critical` (crash, data loss, security), `high` (wrong result
  on a realistic input), `medium` (wrong only in an edge case), `low` (fragile,
  latent);
- a **`file:line`** reference;
- a **concrete failure scenario** — specific inputs or interleaving that lead to
  the wrong outcome. A finding with no scenario is a guess; drop it.

### Agent 1: Logic & Control Flow

- Off-by-one, wrong comparison or boolean operator, inverted condition.
- Incorrect boundary handling; loops that skip the first/last item or run one too
  many times.
- Branches that can't be reached, or that fall through when they shouldn't.
- Wrong operator precedence; integer division or truncation where not intended.

### Agent 2: Nil, Errors & Return Values

- Dereferencing a value that can be nil/null/undefined on some path.
- Errors that are swallowed, logged-and-continued when they should stop, or
  returned without wrapping context.
- Ignored return values that carry an error or a "not found" signal.
- Early returns that leave state half-updated.

### Agent 3: Concurrency & State

- Shared state read/written without synchronization; data races.
- Check-then-act races (TOCTOU) on files, maps, or shared fields.
- Deadlocks, lock ordering, holding a lock across a blocking call.
- Goroutine/task leaks; work started but never awaited or cancelled.
- Mutation of a value another reference still assumes is unchanged.

### Agent 4: Resources, Boundaries & Contracts

- Files, connections, handles, subscriptions opened but not closed on every path.
- Use-after-close / use-after-free; double free/close.
- Edge inputs: empty, zero, negative, very large, overflow, unexpected type.
- API misuse: violating a precondition of a function being called, or breaking an
  invariant a caller relies on.

## Phase 3: Verify, Then Report

Aggregate the findings. Before reporting each one, check it against the actual
code once more and discard anything you cannot tie to a concrete failure — false
positives cost the reader more than a missed nitpick. Deduplicate findings that
point at the same root cause.

**Report findings first**, ordered by severity, each as: `file:line` — one-line
statement of the bug — the failure scenario. Keep any summary short and after the
list. If nothing survives verification, say so plainly and name any residual risk
or gap in test coverage.

## Phase 4: Fix (on request)

If `--fix` was passed, or the user asks to fix after seeing the report, apply the
smallest change that removes each confirmed bug — nothing else (no refactors, no
cleanup; that is `simplify`'s job). Re-state what you changed. Where a fix is not
obvious or has trade-offs, present the options and ask rather than guessing.
