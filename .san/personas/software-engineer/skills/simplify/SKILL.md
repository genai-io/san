---
name: simplify
description: >-
  Review changed code for reuse, quality, and efficiency, then clean up what you
  find — duplicated logic that could reuse an existing helper, hacky or redundant
  patterns, needless work on hot paths. Quality cleanup only: not bugs (that's
  code-review) and not behavior-preserving restructuring on request (that's
  refactor). Use when the user says "simplify", "clean this up", "is there a
  simpler way", or "tidy the diff".
allowed-tools:
  - Bash
  - Read
  - Glob
  - Grep
  - Edit
  - Agent
argument-hint: "[focus area]"
---

# Simplify: quality cleanup on the changed code

Review the changed code for cleanups it would benefit from, then apply them. This
is the **quality** pass — reuse, hacky patterns, wasted work. It is not the bug
pass (`code-review`) and not a requested restructuring (`refactor`); stay in its
lane and don't report correctness bugs or propose structural rewrites here.

## Phase 1: Identify changes

Obtain the complete changed-code diff exactly once in the main conversation. Use
`git diff HEAD` so staged and unstaged changes are reviewed together. With no git
changes, review the most recently modified files the user named or that you edited
earlier in this conversation.

## Phase 2: Review across three lenses (one parallel batch)

Use the Agent tool to launch exactly three subagent reviewers concurrently in one
foreground batch: Reuse, Quality, and Efficiency. Omit `name` to use the default
agent and set `mode=explore` for all three. Include the complete diff text from
Phase 1 directly in every reviewer prompt; do not ask a reviewer to run `git diff` or recover
omitted context itself. Each returns a short list of concrete, located findings.

Launch no second reviewer batch. If a reviewer reports a missing tool, capability,
or context failure, record that reviewer as unavailable and continue with the
other results and your own review. Do not retry it, select another agent, or delegate
the same lens again.

- **Reuse.** New code that duplicates an existing helper or utility; hand-rolled
  logic (string munging, path handling, env checks, type guards) that the
  codebase already has a function for. Point to the existing one to use instead.
- **Quality.** Redundant state that could be derived; parameter sprawl instead of
  restructuring; near-duplicate blocks that should share an abstraction; leaky
  abstractions; stringly-typed code where a constant/enum exists; needless wrapper
  layers; conditionals nested 3+ deep (flatten with early returns or a lookup);
  comments that narrate WHAT instead of non-obvious WHY.
- **Efficiency.** Redundant computation or repeated reads; independent work run
  sequentially that could be parallel; new blocking work on a startup or
  per-request hot path; unconditional no-op updates in loops/handlers (guard with
  change detection); pre-checking existence before operating (TOCTOU); unbounded
  growth or leaked resources; reading/loading far more than needed.

## Phase 3: Apply the cleanups

Aggregate the findings and fix each directly. Keep each fix minimal and matched to
the file's existing style. If a finding is a false positive or not worth it, skip
it — don't argue with it. When done, briefly summarize what you cleaned up, or
confirm the code was already clean.
