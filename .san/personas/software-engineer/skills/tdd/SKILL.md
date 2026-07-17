---
name: tdd
description: >-
  Test-driven development — write a failing test that names the behavior, watch
  it fail, implement the minimum to make it pass, then refactor with tests green.
  Works for new features, bug fixes, and behavior changes. Use when the user says
  "tdd", "test-first", "write tests first", or wants a change built test-first.
allowed-tools:
  - Bash
  - Read
  - Glob
  - Grep
  - Edit
  - Agent
argument-hint: "[feature or behavior]"
---

# TDD: red, green, refactor

Write the test before the code. A test written first specifies the behavior,
fails for a known reason, and gives you a definition of done you can run. A test
written after tends to describe whatever the code happens to do — bugs included.

## Step 1: Find the test runner

Don't assume the command. Detect how this project runs and writes tests: look at
existing test files for the framework and conventions, and at the build/config
for the invocation (`go test ./...`, `npm test`, `pytest`, `cargo test`, …).
Match what's already there — same framework, same layout, same style.

## Step 2: Name the behavior as tests (RED)

State the change as concrete cases before writing any implementation:

- The main success path — the behavior actually requested.
- The edges — empty, zero, boundary, maximum, unexpected input.
- The failures — what should error, and how it should error.

Write these as tests. For a bug fix, the first test **reproduces the bug**. Keep
each test focused on one behavior with a clear name.

## Step 3: Watch them fail

Run the tests and confirm they fail — and fail for the *right* reason (the
behavior is missing), not a typo or an import error. A test you never saw fail
proves nothing. This is the gate: do not write implementation until you've seen
red.

## Step 4: Implement to green

Write the **minimum** code that makes the tests pass. No features the tests don't
demand, no speculative abstraction. Run the tests until they're green.

## Step 5: Refactor on green

Now improve the shape — naming, duplication, structure — with the tests staying
green the whole way. The tests let you clean up without fear; if one goes red, the
last change altered behavior, so undo it.

## Test what callers see, not how it's built

- Assert on observable behavior and return values, not private internals — tests
  bound to implementation break on every refactor and defeat Step 5.
- Keep tests isolated: no shared mutable state, no ordering dependency, each runs
  alone. Prefer real behavior; mock only what you can't run (network, clock,
  external services).
- Cover the edges and error paths, not just the happy case — that's where the
  bugs the persona is trying to prevent actually live.
