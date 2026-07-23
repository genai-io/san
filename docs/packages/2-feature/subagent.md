---
package: github.com/genai-io/san/internal/subagent
layer: feature
---

# subagent

Executor for the single implicit subagent launched through the `Agent` tool.
Each invocation creates an isolated `core.Agent` loop; there is no user,
project, persona, or plugin registry of selectable agent types.

## Purpose

Where [`agent`](agent.md) owns the main conversation session, this package owns
one-shot worker runs. A foreground subagent blocks the spawning tool call and
returns its result. A background run executes under `task.AgentTask`, registers
with the [`broker`](broker.md), and can receive `SendMessage` updates while it
runs.

Multiple workers may run concurrently. They share the current working directory
but have isolated conversations and lifecycle hooks.

## Contract

```go
func NewExecutor(provider llm.Provider, cwd, parentModelID string, hooks hook.Handler) *Executor

func (e *Executor) Run(ctx context.Context, req tool.AgentExecRequest) (*AgentResult, error)
func (e *Executor) RunBackground(req tool.AgentExecRequest) (*task.AgentTask, error)
func (e *Executor) SetParentPermissionMode(getMode func() PermissionMode)
```

The model-facing request has no `subagent_type`. The only selectable runtime
policy is `mode`:

- `default` dynamically inherits the parent session's effective mode when the
  run starts, including bypass mode.
- `explore` is an authoritative read-only ceiling.
- `edit` uses the accept-edits policy.

`bypass` is an internal permission value, not a model-facing request option.
The default and minimum `max_steps` are both 500; larger explicit values are
honored.

## Internals

- `executor.go` — run preparation, model routing, tool construction, permission
  gate, reminders, and lifecycle hooks.
- `executor_prompt.go` — worker identity, display names, and activity labels.
- `executor_run.go` — LLM loop event aggregation and progress reporting.
- `executor_session.go` — transcript persistence and result attribution.
- `adapter.go` — projection onto the Agent tool's executor interface.
- `activity_tools.go` — streams worker tool calls into parent-visible activity.

The tool set is fixed by the effective mode. Calls that would require an
interactive approval are denied because no user is attached to a worker loop.
The root/home removal circuit breaker and parent-only tool boundary remain in
force even when a worker inherits bypass mode.

## Lifecycle

1. The app creates an `Executor` and supplies a thread-safe parent permission
   getter.
2. `Run` resolves the request mode and model, creates a fresh `core.Agent`, and
   fires `SubagentStart` with the fixed protocol identity `subagent`.
3. The worker receives the skills directory; edit-capable runs also receive
   project instructions.
4. Every exit path persists available conversation state and fires
   `SubagentStop` with the same agent id.

The agent model is flat: `Agent` is parent-only, so workers cannot spawn more
workers.

## Tests

```
internal/subagent/executor_test.go   — run config, mode inheritance, permissions,
                                       model routing, lifecycle, persistence.
internal/subagent/scenarios_test.go  — fixed permission-mode truth table.
tests/integration/agent/             — end-to-end Agent execution.
```

## See Also

- Message routing: [`broker`](broker.md)
- Parent agent: [`agent`](agent.md)
- Spawning tools: [`tool`](tool.md)
- Permissions: [`../../concepts/permission-model.md`](../../concepts/permission-model.md)
