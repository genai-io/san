---
name: gain
description: Evaluate the net engineering benefit of a San PR, commit, or working-tree change. Use when asked whether a change was effective, improved maintainability, or introduced regressions.
argument-hint: "<PR | commit | base...head | working-tree>"
---

# Gain

Give two answers: did the change accomplish its intended outcome, and did it improve engineering quality? A useful bug fix can leave maintainability unchanged. Review without modifying the change.

## Establish the comparison

Identify the goal, base, head, affected users/callers, and behavior that must remain unchanged. For a PR compare its merge base to its head; for a commit compare its parent (state which parent for a merge); for a working tree compare staged and unstaged changes to HEAD and identify relevant untracked files. Do not include unrelated local edits in a commit review. If no target is identifiable, ask. Inspect callers and the owning package, not just changed lines. If tests run against a different revision, disclose that limitation.

## Check outcomes and costs

- **Behavior:** Compare representative before/after scenarios against the intended contract. Cover the failure, cancellation, or recovery paths affected. For TUI changes check visible information, input/focus and constrained layouts where relevant. Distinguish a deliberate behavior change from collateral loss. A passing build alone is not evidence of parity.
- **Maintenance:** Describe the next realistic edit: how many independent rules, modules and callers must change before versus after? Count duplicated decisions, not similar-looking lines. Report relevant line deltas as context only.
- **Structure:** Follow one normal and one exceptional path. Did nesting actually disappear or move behind helpers? Check ownership, dependency direction, and coupling against `docs/reference/package-map.md`, `docs/reference/dependency-rules.md` and `docs/design/principles.md`. Keep San policy/UI in San and reusable agent primitives in sdk-go. For changed internal imports run `go run ./tools/layercheck` when feasible.
- **Names and extension:** Do package, file and symbol names express the same business vocabulary as their callers? Test an abstraction against a concrete second use; weigh the cost of a new concept against the edits it removes. Do not reward speculative flexibility.
- **Runtime:** Identify affected hot paths. For plausible CPU, latency or memory impact, compare equivalent workloads and environments across revisions. For Go microbenchmarks collect repeated samples (`-benchmem -count=10`) and compare with `benchstat`; distinguish elapsed time and allocated bytes from CPU usage and peak resident memory, profiling those separately when relevant. Report magnitude, noise and practical impact. For low-risk changes explain why measurement was not necessary; do not claim a measured improvement.

## Decide and report

| Engineering verdict | Rule |
|---|---|
| Positive | Concrete improvement outweighs named costs, with no material unintended regression. |
| Neutral | Sufficient evidence shows no material net change; not a fallback for missing evidence. |
| Negative | A confirmed material unintended regression, or added maintenance/runtime cost outweighing benefits. |

If a missing check could reverse the verdict, report **insufficient evidence** and the smallest check that resolves it. Do not average scores, count improved files as votes, or count one benefit twice. Scope claims to what was checked.

Report in the user's language: **target and baseline; goal met / not met / uncertain; engineering verdict; decisive before → after evidence; costs, checks run and gaps**. Cite changed symbols or paths. Lead with the verdict.

## Basis

- [CodeScene delta reviews](https://github.com/codescene-oss/codehealth-mcp/blob/HEAD/docs/tools.md): improved/stable/degraded per file; use as evidence, not a whole-PR vote.
- [Google review standard](https://github.com/google/eng-practices/blob/HEAD/review/reviewer/standard.md): judge overall code health, not perfection of each line.
- [Go benchstat](https://github.com/golang/perf/blob/HEAD/cmd/benchstat/main.go): repeated before/after performance comparisons.
