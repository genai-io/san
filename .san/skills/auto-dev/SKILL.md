---
name: auto-dev
description: Find engineering problems within an agreed scope, test one change at a time, and commit only changes with a demonstrated net benefit.
argument-hint: "<scope> [stop conditions, e.g. at most 100 rounds or plan quota remaining ≤ 20%]"
---

# Auto Dev

Start with a baseline. Test one hypothesis per round. Only a change you keep becomes the baseline for the next round.

## Set up the run

Record three decisions in the round 0 ledger entry:

- **Scope:** What is the goal, where will you look for candidates, and what may you change? Stay within the agreed scope.
- **Criteria:** What improvement can you verify against the baseline? Which behaviors must not regress? Choose checks that capture the intended benefit and any trade-offs in behavior, maintenance, or performance.
- **Stop conditions:** Record any agreed target, round limit, or quota threshold. Stop when any one is met. Do not invent a round or quota limit if none was specified. Each candidate counts as one round as soon as you start it, whether or not you keep it. The baseline does not count. With a 100-round limit, do not start round 101.

For example, to improve replay, use a long-session profile to find candidates, then compare latency, peak memory, and message order on the same session. Meeting the agreed latency target can end the run; finding that one candidate did not improve latency ends only that experiment.

Record the starting SHA and validation commands. Create a separate worktree and a new `auto-dev/<run>` branch at that SHA. Uncommitted changes in the original workspace are not part of the baseline. If the goal depends on them, pause for clarification. Follow the repository guidance for the target package. Run the checks on the unchanged baseline and record existing failures. If the baseline or validation environment is unavailable, stop; this is not a failed candidate.

## Enforce a quota threshold

If the user sets a remaining-quota threshold, identify the account, plan, and applicable quota window before starting. If several windows apply and the user has not specified one, ask. Use only a trustworthy, sufficiently recent reading of the percentage remaining in that window. Local token counts, context usage, and cost estimates are not substitutes.

Check before starting the run, before each round, and before any expensive step. If the reading is at or below the threshold, start no more work. Record the reason and the current round's state. If you cannot get a reliable reading, pause rather than waive the limit. Polling cannot guarantee an exact reserve: one request may cross the threshold. Treat an unfinished round as interrupted, not discarded.

## Run each experiment

1. **Search without editing.** Use the agreed candidate sources. By default, review engineering issues; also review model context when it is involved. Prefer reproducible problems with clear impact and a small scope. If one module yields nothing, inspect other unreviewed paths within scope. Declare the search exhausted only when the whole agreed scope has no verifiable new candidate. Do not invent refactors to fill rounds. Keep security checks defensive: record sensitive findings for human review without reproducing attacks. The [audit guide](../audit/SKILL.md) offers optional review techniques; this procedure does not require it.
2. **Choose one hypothesis.** Split a compound problem only if each part has an independent acceptance test and benefit. Include prerequisite work with the change it enables if it has no benefit alone. Before editing, open a `pending` ledger row with the current base, problem and location, expected gain, scenarios to protect, and pass/fail criteria. Reproduce the problem on the baseline when possible. If you add a regression test, run the same test against both implementations. Do not weaken assertions, change the workload, or move the acceptance criteria to make a candidate pass.
3. **Make the smallest useful change.** Start from the latest retained commit (or the starting SHA for the first candidate) in the clean experiment worktree. Change only what the problem requires, including necessary tests and documentation. Leave unrelated cleanup alone. If the fix requires broader scope or a change to the product contract, record it and refer it to a human.
4. **Evaluate independently of the hypothesis.** Compare the entire candidate diff, including new files, with this round's base. Rerun the same checks under the same conditions. Record goal attainment and the effects on behavior, maintenance, and runtime separately. Add interaction checks or repeated benchmarks as risk warrants; distinguish inference from measurement. Judge the observed results, not the author's expectations. The [gain guide](../gain/SKILL.md) offers optional evaluation techniques.
5. **Decide from the evidence.** Compare failures with the baseline. A failure on both sides is not a new regression, but if it obscures a deciding criterion, mark the result `inconclusive` and identify the missing evidence. You may fix an obvious implementation typo in this round and rerun the original checks. If a like-for-like comparison refutes the hypothesis, record `discard`; more passing tests do not justify repeated attempts. A different hypothesis needs a new round.
   - `keep`: The candidate meets its goal, delivers a verifiable material gain in at least one dimension, and has no unacceptable side effects. Fixing a user problem counts even if engineering quality is unchanged. Stage only this round's files, inspect the staged diff, and commit as described below.
   - `discard`: The goal was not met, or the net benefit is zero or negative.
   - `inconclusive`: Missing evidence could change the decision. Gather it within budget if possible; otherwise do not commit.
6. **Close the round and continue.** Save the evidence before undoing anything. For a non-keep result, undo only this round's changes using the procedure below. Update the same ledger row, then verify HEAD, the index, and the worktree before continuing. Never retry a hypothesis without new evidence. A progress report, a round verdict, or a completed subtask is a checkpoint, not a reason to stop. Unless a stop or pause condition applies, check the quota if applicable and continue the search.

## Experiment ledger

Create a new `<git-common-dir>/auto-dev/<run>/results.tsv` for each run. Find the absolute common directory with `git rev-parse --path-format=absolute --git-common-dir`. Keep the ledger out of product commits. Use this header, with literal tabs between fields and no line breaks within a field:

    round	base	hypothesis	criterion	behavior	maintenance	runtime	status	commit	evidence

Use one row per round. Round 0 has status `baseline`; record the starting SHA, run decisions, checks, and existing failures. For a candidate, write the `pending` row before editing and update it in place on completion. Record the comparison in each outcome column; say when an effect is unknown or unmeasured. In `evidence`, give the actual commands and before/after results, or point to a per-round record in the same directory. Never record credentials. For a candidate you did not keep, record what you tried, what refuted it or what evidence was missing, and what new evidence would warrant another attempt.

For a `keep`, use the repository's usual commit subject style. Explain the problem, key before/after evidence, and anything unverified in the body. End with the trailer `Auto-Dev: <run>/<round>`. After the commit succeeds, enter its actual SHA in the row's `commit` field.

## Undo changes and recover from interruptions

- **Uncommitted candidate:** In the experiment worktree, confirm the branch, verify `HEAD == base`, and establish ownership of each changed path. For existing files changed by this round, use explicit paths: `git restore --source=<base> --staged --worktree -- <paths>`. Unstage any staged new files first, then remove only files confirmed to have been created by this round. Never reset or clean the whole tree or use the original workspace for rollback. Continue only if HEAD is unchanged and `git status --porcelain` is empty. If any change is unfamiliar or ownership is uncertain, leave it intact, stop, and report it.
- **Regression in a retained commit:** Stop the loop. Report the affected SHA and any later changes that depend on it. With user confirmation, run `git revert <sha>` on the experiment branch, revalidate, and append a `revert` row. Set `base` to HEAD before the revert and `commit` to the revert commit; in `evidence`, cite the original round, SHA, and reason. Keep the original row, do not rewrite shared history, and refer conflicts to a human.
- **Interrupted round:** `pending` does not mean `discard`. Check the branch, HEAD, worktree, and commit trailers. If the commit succeeded before the ledger was updated, verify its parent and validation record, then complete the row without committing again. If HEAD is still at the round's base and ownership is clear, resume validation or follow the uncommitted-candidate rollback procedure. Otherwise stop and reconcile the state before doing more work.

## Finish the run

Stop when an agreed condition is met, the whole scope has no verifiable new candidate, the environment blocks the work, or the user asks you to stop. Respect quota and interruption rules even during final checks.

If multiple retained changes affect the same user path, compare the starting SHA with final HEAD using the same scenario. Add the combined result to the last relevant round's `evidence`. Follow the retained-commit revert procedure for a regression. If you cannot rerun the scenario, report the gap; individual passes do not establish a combined pass.

Report the branch, ledger path, retained commits, attempts not kept, reason for stopping, and remaining validation gaps. Do not automatically push, merge, or release.
