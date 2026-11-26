# Engineering quality

Set the scope and workload, trace real workflows, verify possible problems, then report them in order of impact. Use the review topics to find problems and severity to rank them. Do not report a finding twice.

This guide draws on [DietrichGebert/ponytail's ponytail-audit](https://github.com/DietrichGebert/ponytail/blob/main/skills/ponytail-audit/SKILL.md) for scoping the review, verifying findings, and reporting in repair order. Judge structure and naming against San's own responsibilities and terminology.

## 1. Set the scope and expected workload

1. Honor the requested directory, package, or repository. If none is specified, review the whole repository. Use `docs/reference/package-map.md` and the actual entry points to identify the main components. Follow dependencies outside the scope when needed to understand a call, but do not turn a local review into a repository-wide one.
2. Use the documentation, startup paths, and callers to establish who runs the system, how many tasks might run at once, and whether they share files, databases, or other state. One user may start several sessions or processes. Even on one thread, background tasks can take turns accessing the same resource. Assess capacity, isolation, and consistency of writes against a workload you can justify. Separate design promises, assumptions, and measurements. If the workload is unknown, say when your conclusion would change.
3. Follow each main workflow from a real entry point to its final output. Identify data sources, validation, stored state, and what remains after failure. Trace paths involving user input, money, authentication, authorization, writes, background jobs, or resources shared across processes all the way through. For a background write, follow submission through the worker to storage and the result returned to the caller.
4. Spend the most time on paths where failure is costly, affects many users or components, or is likely under contention. You need not read every file in a large repository. Introduce the scope and workload before the findings. Identify the paths you actually traced, what you did not read, and how that limits the conclusions. Mark incomplete paths as not covered. Do not generalize from a sample to the whole repository or describe planned checks as completed.

## 2. Trace the workflows

For each root cause, identify the code location, trigger, observed or expected impact, smallest change, and way to verify it. Distinguish confirmed problems from risks that depend on assumptions and from optional improvements.

### Behavior and cost

- **Correctness:** Look for wrong results, crashes, and missed boundaries, especially nulls, zero, the last item, rounding, and time zones. Compare each return value, including empty results and errors, with how its caller handles it. Where several entry points implement the same rule, compare their behavior on the same boundary input.
- **Security and data:** At trust boundaries, check input validation, injection risks, predictable randomness used for security, and hardcoded secrets. Report a secret's location, never its value. Follow writes to find hidden failures, incorrect ordering, and groups of writes that should succeed or fail together but lack a transaction or equivalent guarantee. Explain what a partial failure would lose or leave inconsistent.
- **Concurrency and scale:** At the expected load, look for races between checking for an item and creating it, jobs repeated by multiple processes, and state kept in one process when others need to share it. Check for unbounded memory or list growth, one database query per item, and quadratic work on large inputs. Identify the order of operations or amount of data that triggers the problem. A passing single-process test does not establish safety across processes.
- **Missing tests:** For critical branches, parsers, monetary calculations, security checks, and writes, ask which assertion would fail if the rule broke. If none would, give an input and an expected result or stored state that distinguishes correct behavior from the error. Attach a regression test to the defect it covers rather than counting it as another finding.
- **Performance:** Use real call frequency, input sizes, and latency budgets. Report a clear slowdown on a critical path as a problem. A small improvement such as avoiding repeated work in a hot loop is usually a suggestion, but it can matter to acceptance when the budget is milliseconds. Distinguish estimates from measurements. For an unmeasured change, give a reproducible benchmark rather than promising a gain.

### What complexity can go?

- Recommend deleting dead code, unused options, flags, configuration, or speculative features only after ruling out callers and external contracts.
- If two helpers do the same thing, name the one to keep and its file. Consider merging similar implementations that must change together, but not ones that merely look alike and change for different reasons.
- Before adding a dependency, compare its behavior, edge cases, and maintenance cost with standard library features, platform facilities, or a few clear lines of code.
- For single-implementation interfaces, single-product factories, and forwarding wrappers, check whether they provide isolation, testing support, or compatibility today. Recommend removal only if they serve no real purpose.
- Split a function or class when unrelated responsibilities make it hard to read or test. Do not split by line count or add helpers with no independent responsibility just to shorten a function.

### Does the design keep its promises?

The user can name a mechanism directly, for example `/audit engineering session recovery`. Explain what problem it solves, what it promises to whom, and what falls outside that promise. Use documentation, callers, and tests to distinguish commitments from assumptions.

Trace one real scenario: what triggers the mechanism, who owns its state, when others can see it, and who handles recovery. Choose a boundary case that tests its promise and compare each expected step with the implementation. For example, suppose recovery promises not to repeat completed work after restart. If a process exits after producing an external effect but before saving its completion record, what evidence determines whether it retries? Do not call this a defect if the project makes no such promise.

Say under which conditions the design is reasonable, which specific scenario breaks a promise, or what evidence is missing. If a change is needed, compare the costs, benefits, and verification of the smallest alternative with keeping the current design. Do not add a layer if an existing mechanism suffices. Do not report a reasonable tradeoff as a problem or count the same defect again under concurrency or maintainability.

### Can maintainers locate, change, and test a behavior?

**Structure.** Look beyond local readability. Can a developer find who owns a behavior and change it within a clear boundary without editing unrelated modules? Check the nearest `AGENTS.md`, package map, design principles, and dependency rules against the actual responsibilities and dependency directions of directories, packages, and files.

Trace a concrete change through imports and call sites: which package decides the rule or owns the state, and which packages only consume it? If changing the rule means editing unrelated callers, show where and why. Keep the behavior with its owner; do not add a layer just to make directories look alike. For a proposed move, check the resulting import direction, read the qualified name in a real caller, and identify the owner and caller tests that should still pass.

For a layout review, pick two changes a maintainer might make and name the first place they would look. Changing how a live chat displays Markdown should lead to the view; changing what survives a restart should lead to the session store. A shared caller or a common implementation detail is not, by itself, a reason to merge packages. Recommend keeping the layout if both changes already have clear owners.

For a rename-only change, separate moves from edits in the diff. Check the full path and the name as imported, including any alias needed to distinguish two packages. Shorter is not clearer if the caller has to reconstruct what the name means. Verify behavior at a consumer (for a chat view, render a streamed reply or tool result), not just by compiling the renamed package. Check links and embedded assets that the compiler cannot see.

**Names.** Judge files, types, structs, objects, fields, variables, environment variables, and functions in the context of the project. Consult `docs/reference/file-naming.md`, public contracts, documentation, and major callers. For example, trace an environment variable through its configuration field to the consuming function: do all three express the same concept and unit? Inspect key names only, never environment variable values.

Prefer short, concrete names that describe a stable responsibility. Would the name still fit another reasonable caller tomorrow? Avoid both today's call-site details and vague names for imagined future uses. Layers may follow different spelling conventions. Clear local variables do not need a project prefix.

For each proposed rename, compare the old and new names at a call site. Identify the project term the new name uses and the ambiguity it removes. Check compatibility with scripts, docs, stored fields, and public interfaces. If mixed responsibilities are the real problem, report a structural finding; do not count the resulting rename separately.

## 3. Verify each possible finding

1. Give a location and a concrete example: input or scenario, expected behavior, and actual result. For concurrency, show the order of operations. For maintainability, describe a specific change and its extra cost. Distinguish observed results from conclusions drawn by reading code. Without an example, do not report a problem.
2. Before calling code unused, search the entire repository even for a single-package review. Check source, tests, fixtures, configuration, scripts, and docs, then check runtime lookups such as registries, aliases, string keys, and reflection. Do not claim code is unused if you cannot rule out dynamic consumers or external contracts.
3. For an intentional simplification marked `shortcut:` (formerly `ponytail:`), report a problem only if the expected workload exceeds its stated limit and you can identify a concrete consequence. Hypothetical growth is not evidence.
4. Find the smallest fix. Prefer deletion when it solves the problem. Avoid unrelated abstractions, frameworks, or settings. Explain what to change, why it works, and how to verify it.
5. Drop personal preferences, vague suggestions, and concerns with no clear consequence. Record missing evidence as a review limitation, not a defect.

## 4. Report by impact

Report problems and proposed fixes without changing code. Write short sentences in everyday English for someone who has not read the code. Explain technical terms on first use. Do not group findings by review topic.

Start with `What this repo does:`. In two or three sentences, describe the project's purpose and the scale of use you assessed. For a partial review, name its scope without implying you checked the whole repository.

Use these groups in order. Omit empty groups and put the most important items first within each:

- `Must fix`: Wrong results, security vulnerabilities, data loss, or failure under the expected load.
- `Should fix`: Untested high-risk logic, clear slowdowns, duplicate code, mixed responsibilities, or genuinely unnecessary code.
- `Nice to have`: Small speed gains or simplifications that preserve behavior.

Number findings continuously across groups so readers can refer to them by number. List at most 20. If there are more, keep the highest-impact findings and state how many verified smaller ones you omitted. Start each with a short English title and a real code location, for example `1. Orders get the wrong date (billing/close_day.py:L40-52)`. Follow with four lines of one or two sentences each:

- `What this is:` Explain what the code does in the workflow.
- `Problem:` Give the input or condition and the result that differs from what was expected.
- `Fix:` Describe the smallest change that solves the problem.
- `If we skip it:` State the concrete consequence for users, data, or maintenance.

End with a one-line `Verdict:` on the overall condition or which item to fix first. Include `Lean:` only if you found a real opportunity to simplify. Estimate the net lines and number of dependencies that could be removed, without double counting. Finish with `Not checked:` listing unread areas and checks you could not run.

If you found no problems, skip the groups and the usual closing lines. Keep `What this repo does:`, then write `Healthy. Nothing to fix.` on its own line. Finish with one `Checked:` line naming the scope actually checked and any important gaps.
