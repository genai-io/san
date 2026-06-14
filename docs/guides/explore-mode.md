# Explore Mode

## Overview

`explore` is the subagent permission mode for fast, non-mutating codebase investigation. Use it with `general-purpose` when a task requires reading, searching, and cross-referencing multiple files before answering.

This feature exists to document the contract that was previously split across the general agent docs, non-mutating mode docs, and subagent notes.

## Contract

| Property | Value |
|----------|-------|
| Agent type | `general-purpose` |
| Permission mode | `explore` |
| Tools | Read, Glob, Grep, WebFetch, WebSearch |
| Max turns | 100 |
| Execution style | Foreground in explore mode |

**Use `mode=explore` when:**
- The task needs reading multiple files before answering.
- The task needs cross-referencing code paths, configs, tests, and docs.
- The task is investigative and should not modify the workspace.

**Do not use `mode=explore` when:**
- One direct tool call is enough, such as a single `Read`, `Grep`, or `Glob`.
- The task needs file edits or command execution.
- The task should run in the background from an exploration-only context.

## Behavior

- The agent inherits the parent model unless explicitly overridden.
- In explore mode, the subagent tool schema only exposes non-mutating tools.
- Explore mode must not expose `Bash`, `Write`, or `Edit`.
- The agent returns a normal agent tool result to the parent conversation; the parent conversation must continue cleanly after the result arrives.
- Interleaved `notice` messages must not prevent the parent conversation from recognizing that the agent finished.

## Relationship To Other Features

- [Writing a Subagent](writing-a-subagent.md) covers the generic agent system and custom agent format.
- `explore` is the investigative permission boundary used by subagents.

## Automated Tests

```bash
go test ./internal/subagent -run TestExploreModeFiltersMutatingToolSchemas -count=1
go test ./tests/integration/agent -run TestAgent_ExploreMode_BlocksWrites -count=1
```

Covered:

```
TestExploreModeFiltersMutatingToolSchemas                — explore mode only exposes non-mutating tools
TestAgent_ExploreMode_BlocksWrites                       — explore mode blocks writes in agent execution
TestPlanModeAgentExecutionStartsContinuationWithoutHanging — explore result resumes the parent flow
TestHasAllToolResultsAllowsInterleavedNotices           — notice messages do not break completion detection
TestAsyncHookTickDoesNotInjectWhileToolExecutionPending — async hook cannot interrupt pending agent execution
TestCronTickDoesNotDrainQueueWhileToolExecutionPending  — cron cannot interrupt pending agent execution
```
