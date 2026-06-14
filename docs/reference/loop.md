# Loop Scheduling Command

## Overview

`/loop` is the user-facing scheduling workflow built on top of the cron system.

It provides:

- Recurring scheduled prompts
- One-shot scheduled prompts
- Task management commands for listing and deleting scheduled jobs
- Immediate execution for recurring `/loop ...` requests
- Preserved slash-command visibility in the TUI transcript

`/loop` is implemented as a slash command in the interactive TUI and uses the cron store/scheduler under the hood.

Current scope:

- `/loop` is an interactive TUI feature
- `/loop` creates session-only scheduled jobs
- Durable persistence, catch-up, and scheduler recovery are currently provided by the underlying cron subsystem, not by extra `/loop` flags

## Command Forms

| Command | Function |
|---------|----------|
| `/loop 5m <prompt>` | Schedule a recurring prompt every 5 minutes and execute it immediately |
| `/loop <prompt> every 20m` | Schedule a recurring prompt using trailing interval syntax and execute it immediately |
| `/loop once 20m <prompt>` | Schedule a one-shot prompt 20 minutes from now |
| `/loop once <prompt> in 20m` | Schedule a one-shot prompt using trailing interval syntax |
| `/loop list` | List scheduled loop jobs |
| `/loop delete <id>` | Delete a scheduled loop job |
| `/loop delete all` | Delete all scheduled loop jobs |

## Behavior

### Recurring

- Recurring `/loop ...` creates a cron job.
- The parsed prompt is executed immediately in the current session.
- The literal `/loop ...` input remains visible in the TUI transcript.
- Recurring jobs auto-expire after 7 days.

### One-Shot

- `/loop once ...` creates a one-shot cron job.
- It does not execute immediately.
- It fires once and is then automatically removed.

### Scheduling Semantics

- Minute-based recurring intervals map to 5-field cron when cleanly representable.
- Non-clean intervals are rounded to the nearest supported cadence, and the user-visible note explains the adjustment.
- Recurring schedules avoid clustering through:
  - off-minute generation in `/loop`
  - deterministic bounded jitter in the cron runtime
- `/loop` itself currently schedules session-only jobs. Durable catch-up behavior applies to cron jobs created through the lower-level cron subsystem with `durable=true`.

## Support Matrix

| Mode | Support |
|------|---------|
| Interactive TUI | Supported |
| Non-interactive `san "..."` | `/loop` slash-command workflow is not supported |

## Related Features

- Slash command dispatch: [`slash-commands.md`](slash-commands.md)
- Cron store and scheduler internals: [`packages/cron.md`](../packages/2-feature/cron.md)

## Automated Tests

```bash
GOCACHE=/tmp/gocache go test ./internal/app/... ./internal/cron ./internal/tool/...
```

Covered by automated tests:

```text
TestExecuteCommandLoopSchedulesRecurringPrompt          — recurring /loop creates job and immediately executes prompt
TestExecuteCommandLoopParsesTrailingEveryClause         — trailing "every" syntax supported
TestParseLoopCommand_RoundsNonCleanIntervals            — non-clean recurring interval rounding
TestParseLoopCommand_AvoidsTopOfHourScheduling          — recurring schedules avoid :00 clustering
TestExecuteCommandLoopListAndDelete                     — list/delete management flow
TestExecuteCommandLoopDeleteAll                         — bulk delete flow
TestExecuteCommandLoopOnceSchedulesOneShot              — one-shot /loop scheduling
TestParseLoopOnceCommand_SupportsTrailingInClause       — trailing "in" syntax supported
TestHandleCommandSubmit_LoopDeleteAllPreservesLiteralInputAndDeletesJobs
                                                       — TUI submit path keeps literal `/loop delete all` and removes all jobs
TestShouldPreserveCommandInConversation_PreservesLoopKeyword
                                                       — `/loop` remains visible in transcript
```

Automation split:

- `./internal/app/...`
  - slash-command parsing
  - immediate execution for recurring jobs
  - transcript visibility rules
  - `/loop list` and `/loop delete`
  - `/clear` interaction with scheduled jobs
- `./internal/cron`
  - durable persistence/load
  - one-shot catch-up after restart
  - recurring jitter bounds and determinism
  - recurring reschedule persistence
- `./internal/tool/...`
  - cron tool schema stays aligned with project storage semantics

## Interactive Regression Tests

Interactive TUI is the primary product path for `/loop`. Manual regression should verify both user-visible behavior and scheduler side effects.

Important:

- Commands that contain words like `delete` should be injected with `tmux set-buffer` + `tmux paste-buffer` during manual testing.
- Do not rely on plain `tmux send-keys` for `/loop delete ...` coverage: some tmux setups treat `delete` as a special key token and create a false failure.
- For `/loop` admin commands, the expected UI behavior is explicit literal preservation: the transcript should show the full slash command the user typed, not a shortened or rewritten form.
- This same literal-preservation rule should hold for slash commands globally: command names and trailing arguments should remain visible in the transcript unless the command intentionally clears or exits the session.

```bash
tmux new-session -d -s t_loop -x 220 -y 60
tmux send-keys -t t_loop 'san' Enter
sleep 2

# Test 1: recurring loop stays visible and executes immediately
tmux send-keys -t t_loop '/loop 5m check the deploy' Enter
sleep 3
tmux capture-pane -t t_loop -p
# Expected:
# - transcript still shows literal "/loop 5m check the deploy"
# - recurring task is scheduled
# - parsed prompt starts running immediately
# - notice mentions cadence, cron, and auto-expiry

# Test 2: list jobs
tmux send-keys -t t_loop '/loop list' Enter
sleep 2
tmux capture-pane -t t_loop -p
# Expected: scheduled task appears with id, cron, prompt, next fire time

# Test 3: one-shot scheduling
tmux send-keys -t t_loop '/loop once 20m check the deploy' Enter
sleep 2
tmux capture-pane -t t_loop -p
# Expected:
# - one-shot task scheduled
# - no immediate assistant run

# Test 4: recurring management command stays visible in transcript
tmux send-keys -t t_loop '/loop list' Enter
sleep 2
tmux capture-pane -t t_loop -p
# Expected:
# - literal "/loop list" remains in the transcript
# - result notice is shown beneath it

# Test 5: delete all
# Use paste-buffer here: some tmux send-keys setups treat "delete" as a special key token
tmux set-buffer '/loop delete all'
tmux paste-buffer -t t_loop
tmux capture-pane -t t_loop -p
# Expected before submit:
# - input line shows the exact literal command `/loop delete all`
# - it must NOT appear as `/loop all` or any shortened form
tmux send-keys -t t_loop Enter
sleep 2
tmux capture-pane -t t_loop -p
# Expected after submit:
# - transcript shows the exact literal command `/loop delete all`
# - result notice confirms all scheduled tasks were removed
# - it must NOT appear as `/loop all` or any shortened form

tmux kill-session -t t_loop
```

## Non-Interactive Regression Notes

`/loop` is not a supported non-interactive slash-command surface today. Non-interactive regression for this feature is therefore about rejecting or avoiding the UX path, while still validating the underlying scheduler components separately.

```bash
# /loop is not a supported non-interactive slash-command interface
san "/loop 5m check the deploy"

# Expected:
# - no documented guarantee that slash commands run in non-interactive mode
# - feature support remains "interactive TUI only"
```

Underlying non-interactive regression targets:

```bash
GOCACHE=/tmp/gocache go test ./internal/app/... -run 'TestExecuteCommandLoop|TestShouldPreserveCommandInConversation|TestHandleClearCommand_PreservesScheduledTasks' -v
GOCACHE=/tmp/gocache go test ./internal/cron -run 'TestStoreDurable|TestStoreTick_DurableRecurringPersistsUpdatedState|TestLoadDurable_OneShotPastDueFiresOnNextTick|TestComputeNextFire_' -v
```
