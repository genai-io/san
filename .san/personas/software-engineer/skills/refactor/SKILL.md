---
name: refactor
description: >-
  Restructure existing code without changing its behavior — extract, rename,
  inline, split, deduplicate, reshape — with tests holding behavior fixed before
  and after. Scoped to structure only: no new features, no bug fixes, no behavior
  changes. Use when the user says "refactor", "clean up the structure",
  "extract this", "split this file", "untangle", or "restructure".
allowed-tools:
  - Bash
  - Read
  - Glob
  - Grep
  - Edit
  - Agent
argument-hint: "[target file or symbol]"
---

# Refactor: restructure without changing behavior

A refactor changes the *shape* of code, never what it *does*. The one rule that
makes it safe: behavior is pinned by tests before you touch anything, and the
same tests stay green after. If behavior needs to change, that is not a refactor
— stop and treat it as a feature or a fix.

## Phase 1: Pin the behavior

Before restructuring, make the current behavior observable and guarded.

1. Find the tests that cover the code you intend to move. Run them and confirm
   they pass. This green run is your baseline — write it down.
2. If the code is not covered, **add characterization tests first**: tests that
   capture what the code does *today* (not what it should do), enough to catch a
   behavior change. Get them green against the current code before you refactor.
3. If you cannot get the behavior under test at all, say so and stop — an
   unguarded refactor is a rewrite in disguise, and you would have no way to know
   you broke something.

## Phase 2: Restructure in small steps

Make one structural move at a time, and keep the tests green between moves.

- Each step is a single transformation — extract a function, rename a symbol,
  inline a variable, split a file, unify duplicated blocks — not a pile of them.
- Run the tests after each step. If they go red, the last step changed behavior;
  undo it and make a smaller move.
- Change structure only. No new parameters "while you're in there", no bug fixes,
  no renamed behavior. If you spot a bug, note it for a separate `code-review`
  pass — do not fix it inside the refactor.
- Match the conventions already in the code; a refactor should look like it was
  always there.

## Phase 3: Verify behavior is unchanged

- Run the full test suite. It must be as green as your Phase 1 baseline — same
  tests passing, none skipped to get there.
- Confirm the public interface callers depend on is unchanged, or if it changed,
  that every call site was updated in the same pass.
- The diff should contain *only* structural change. Every hunk traces to the
  restructuring goal; if a hunk changes behavior, it does not belong here.

Report what you restructured and confirm the baseline tests still pass.
