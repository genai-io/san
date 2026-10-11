`/simplify → 4 cleanup agents in parallel → apply the fixes`

You are improving the quality of the changed code, not hunting for bugs.
Review it for reuse, simplification, efficiency, and altitude issues, then fix
what you find. Do not look for correctness bugs — that is a code review's job,
not this command's.

## Phase 0 — Gather the diff

Run `git diff @{upstream}...HEAD` (or `git diff main...HEAD` / `git diff
HEAD~1` if there is no upstream) to get the unified diff under review. If
there are uncommitted changes, or the range diff is empty, also run `git diff
HEAD` and include the working-tree changes in scope — the review often runs
before the commit. If a PR number, branch name, or file path was passed as an
argument, review that target instead. Treat this diff as the review scope.

## Phase 1 — Review (4 cleanup agents in parallel)

Launch **4 independent review agents** via the Agent tool, all in a single
message with `mode: "explore"` and `run_in_background: false` so they run
concurrently and the tool batch waits for all four results before your next
inference. These reviews block the fixes; do not launch them in the background.
Tell each agent how to reproduce the diff
(the exact git command and directory) and give it ONE of the four angles
below. Each returns its findings with `file`, `line`, a one-line `summary`,
and the concrete cost (what is duplicated, wasted, or harder to maintain).
Tell agents to verify each finding against the current file before reporting.
Each reviewer must distinguish a verified review from work it could not
complete, listing any inaccessible files. Give paths and exact diff commands;
let reviewers inspect source with their own available tools instead of copying
source into their prompts.

### Reuse

Flag new code that re-implements something the codebase already has — search
shared/utility modules and files adjacent to the change, and name the
existing helper to call instead.

### Simplification

Flag unnecessary complexity the diff adds: redundant or derivable state,
copy-paste with slight variation, deep nesting, dead code left behind (a
helper whose last caller the diff removed), parameters nobody uses. Name the
simpler form that does the same job.

### Efficiency

Flag wasted work the diff introduces: redundant computation or repeated I/O,
work re-done per call that could be computed once, independent operations run
sequentially, blocking work added to startup or hot paths. Also flag
long-lived objects built from closures or captured environments — they keep
the entire enclosing scope alive for the object's lifetime; prefer a struct
that copies only the fields it needs. Name the cheaper alternative, and be
honest about whether the cost actually sits on a hot path.

### Altitude

Check that each change is implemented at the right depth, not as a fragile
bandaid. Special cases layered on shared infrastructure are a sign the fix
isn't deep enough — prefer generalizing the underlying mechanism over adding
special cases. Also flag knowledge duplicated across layers that will drift,
and string-matching against message content where a typed value belongs.

## Phase 2 — Apply the fixes

Read all four returned reports. A finished agent run is not evidence that its
review was complete. If a review is blocked, report the missing coverage and
the blocker; do not silently replace it with a clean result or launch another
review with pasted source to get around the restriction.

Dedup verified findings that point at the same
line or mechanism, and fix each remaining one directly. Skip any finding
whose fix would change intended behavior, require changes well outside the
reviewed diff, or that you judge to be a false positive — note the skip
rather than arguing with it. Run the project's tests and linter after the
fixes. Finish with a brief summary of what was fixed and what was skipped
(or confirm the code was already clean).
