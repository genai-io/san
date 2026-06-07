---
name: qa
description: >-
  Regression test a San feature by number or name. Reads the feature doc from docs/features/,
  runs automated Go tests and interactive tmux tests, then produces a pass/fail report.
  Use this skill when the user says "qa", "regression test", "test feature X", "verify feature",
  or references a feature number like "test 01", "qa 6-hooks", "regress 12".
allowed-tools:
  - Bash
  - Read
  - Glob
  - Grep
  - Agent
argument-hint: "<feature> [--interactive] [--pane]"
---

# QA — Feature Regression Testing

Run automated and interactive regression tests for a San feature based on its doc in `docs/features/`.

## Arguments

- `<feature>` — Feature number (e.g. `01`, `6`) or filename stem (e.g. `6-hooks`, `cli-startup`). Partial matches are fine.
- `--interactive` — Also run the interactive tmux tests (default: automated only).
- `--pane` — For interactive tests, split a pane in the **current** tmux window instead of creating a separate session. This lets the user watch the test live side-by-side.

If only a number is given, zero-pad and match against filenames (e.g. `1` → `1-cli-startup.md`).

## Workflow

### 1. Resolve the feature doc

```
docs/features/<N>-<name>.md
```

Read the file. Extract:
- **Automated Tests** section — the `go test` commands and known test cases.
- **Interactive Tests (tmux)** section — the step-by-step tmux script.

If the feature cannot be found, list all available features and ask the user to pick one.

### 2. Build the binary

Always build first so tests run against the latest code:

```bash
make build
```

If the build fails, stop and report the error — no point running tests against stale code.

### 3. Run automated tests

Execute every `go test` command listed in the feature doc's **Automated Tests** section. Capture stdout/stderr and exit codes. Each command is a separate test group.

Record results as:
- **PASS** — exit code 0
- **FAIL** — exit code non-zero; include the failure output

### 4. Run interactive tests (only with `--interactive` or `--pane`)

Skip this phase unless the user explicitly opts in.

#### Pane mode (`--pane`)

Split a pane in the current tmux window for the test:

```bash
# Detect current session and window
TMUX_TARGET=$(tmux display-message -p '#{session_name}:#{window_index}')
# Split horizontally, 50% width
tmux split-window -h -t "$TMUX_TARGET" -l '50%'
# The new pane is at $TMUX_TARGET.{next_pane_index}
```

Run all interactive test steps in that pane via `tmux send-keys`. Use `tmux capture-pane -p` to read the pane output after each step and verify the **Expected** comments from the doc.

#### Session mode (default when `--interactive` without `--pane`)

Create a dedicated tmux session:

```bash
tmux new-session -d -s qa_<feature> -x 220 -y 60
```

Run the interactive test steps there. Kill the session when done.

#### Interpreting interactive results

For each test step:
1. Send the command via `tmux send-keys`.
2. Wait the prescribed `sleep` duration (or a reasonable default).
3. Capture the pane with `tmux capture-pane -t <target> -p`.
4. Check the captured output against the **Expected** comment.
5. Record PASS/FAIL with the captured output as evidence.

After all steps, clean up (kill session/pane, remove temp files) as specified in the doc's cleanup section.

### 5. Generate the report

Print a summary table:

```
## QA Report: Feature <N> — <Name>

### Automated Tests
| Test Group               | Result |
|--------------------------|--------|
| go test ./internal/x/... | PASS   |
| go test ./tests/int/...  | FAIL   |

### Interactive Tests
| Step                         | Result | Notes                    |
|------------------------------|--------|--------------------------|
| Test 1: Basic TUI startup    | PASS   | TUI with input box shown |
| Test 2: Print mode           | FAIL   | No output after 15s      |

### Summary
Automated: 2/3 passed
Interactive: 3/4 passed
Overall: PARTIAL PASS
```

Use **PASS**, **FAIL**, or **SKIP** for each item. Include failure details inline so the user can act on them immediately.

## Important notes

- The binary path is `./bin/san` (built by `make build`), not the installed `san`.
- Interactive tests that require an LLM response need a configured provider. If no provider is set, mark those steps as SKIP with a note.
- For interactive tests, use `Ctrl+C` (`C-c`) to exit `san`, not `q` (which gets interpreted as user input).
- Always clean up temp files and tmux sessions/panes after testing, even on failure.
- If a test step times out (no expected output after the sleep), mark it FAIL and move on — don't hang.
