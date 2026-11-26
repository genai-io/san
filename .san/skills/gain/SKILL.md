---
name: gain
description: Review a PR, commit, or local change. Check whether it solves the problem and whether the benefit is worth the cost.
argument-hint: "<PR | commit | base...head | working-tree>"
---

# Gain

Review the change without editing it. Answer two questions separately: **Did it solve the problem? Is the result better for the project?** A bug fix can be worth keeping even if it neither simplifies the code nor makes it faster.

## Choose the baseline

- **Pull request:** compare the merge base with the PR head.
- **Commit:** compare it with its parent. For a merge, name the parent you chose.
- **Working tree:** compare staged and unstaged changes with HEAD. Include relevant new files; leave unrelated local edits out.

If the target is unclear, ask. Name the affected users or callers, the intended change, and what must still work. Read the owning package and its callers. If tests ran on a different revision, say so.

## Compare like with like

- **Behavior:** Run the same case before and after. Check affected failure, cancellation, and recovery paths. For a terminal UI, exercise the actual keys and rendered view at a relevant width. A passing build does not show that a user can still complete the task.
- **Maintenance:** Pick a likely future change. Which rules, files, and callers would have to change before and after? Count duplicated decisions, not similar lines. Fewer lines alone are not a gain.
- **Design:** Trace a normal path and a failure path. Has the change removed complexity or just moved it? Check package ownership, dependency direction, and names, including filenames. Justify a new abstraction with a concrete second use. In San, keep UI and policy in San and reusable agent primitives in sdk-go. Consult the package map, dependency rules, and design principles; run `go run ./tools/layercheck` when internal imports change.
- **Runtime:** If speed or memory matters, use the same input and environment on both revisions. For Go benchmarks, take repeated samples (`-benchmem -count=10`) and compare them with `benchstat`. Report the size and variability of the difference. Elapsed time is not CPU time; allocated bytes are not peak memory. If measurement is unnecessary, say why rather than claiming a speedup.

## Give a verdict

Lead with the goal (**met**, **not met**, or **uncertain**) and the engineering verdict:

| Verdict | When to use it |
|---|---|
| Positive | The demonstrated benefit outweighs the costs, with no material unintended regression. |
| Neutral | The evidence shows no material net change. |
| Negative | A material regression or added cost outweighs the benefit. |
| Insufficient evidence | A missing check could change the verdict. Name the smallest check that would settle it. |

Report in the user's language. Give the baseline, the decisive before-and-after result, the costs, and what you did not test. Cite the relevant files or symbols. Do not average scores, count files as votes, or count the same improvement twice.

References: [CodeScene delta reviews](https://github.com/codescene-oss/codehealth-mcp/blob/HEAD/docs/tools.md), [Google's code review standard](https://github.com/google/eng-practices/blob/HEAD/review/reviewer/standard.md), and [Go benchstat](https://github.com/golang/perf/blob/HEAD/cmd/benchstat/main.go).
