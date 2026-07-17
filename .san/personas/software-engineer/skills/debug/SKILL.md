---
name: debug
description: >-
  Systematic debugging that finds the root cause before changing any code —
  collect symptoms, trace the code path, check what changed, reproduce
  deterministically, form a testable hypothesis, then fix the cause (not the
  symptom) and prove it with a regression test. Use when the user reports a bug,
  a crash, a failing test, or says "debug", "why is this happening", "root cause".
allowed-tools:
  - Bash
  - Read
  - Glob
  - Grep
  - Edit
  - Agent
argument-hint: "[what's broken]"
---

# Debug: root cause before fix

**The iron law: no fix without a root cause first.** Patching a symptom you don't
understand turns one bug into whack-a-mole — every blind fix makes the next bug
harder to find. Find why it breaks, then fix that.

## Phase 1: Investigate

Gather evidence before forming any hypothesis.

1. **Collect the symptoms.** Read the exact error, stack trace, and the steps
   that trigger it. If you don't have enough to reproduce, ask the user one
   precise question at a time — don't guess your way forward.
2. **Trace the code path.** From the symptom, work backward toward possible
   causes: `Grep` for the call sites, `Read` the logic. Understand what the code
   actually does before theorizing about why it's wrong.
3. **Check what changed.** `git log --oneline -20 -- <affected files>` and read
   the recent diffs. If it worked before, the cause is almost certainly in a
   change — a regression narrows the search to the diff.
4. **Reproduce it deterministically.** Get to a command or test that fails every
   time. If you can't reproduce it reliably, you don't yet understand it — gather
   more evidence before going further. An intermittent bug reproduced is half
   solved.

End Phase 1 with one sentence: **"Root cause hypothesis: …"** — a specific,
testable claim about what is wrong and why, not a vague area of suspicion.

## Phase 2: Test the hypothesis

- Confirm the cause directly: a failing assertion, a log at the suspect line, a
  minimal snippet that isolates it. Prove the hypothesis before acting on it.
- If the evidence contradicts the hypothesis, discard it and return to Phase 1.
  Do not bend the evidence to fit a theory you're attached to.
- Lock scope to the module the root cause lives in. Resist "while I'm here"
  edits to unrelated code — they widen the blast radius and hide the real fix.

## Phase 3: Fix and prove it

1. **Write a test that reproduces the bug and fails** for the right reason.
2. Make the **smallest change that addresses the cause** — not the symptom, not a
   nearby cleanup. If a proper fix is large or risky, present the options and ask.
3. Run the new test (now green) and the surrounding suite (still green). The
   regression test stays, so this exact bug cannot come back silently.

Report the root cause, the fix, and the test that now guards it.
