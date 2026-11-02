---
package: github.com/genai-io/san/internal/agent
layer: feature
---

# agent

Owns the **foreground agent execution lifecycle** — construction, start, stop, and
TUI-facing send/permission/outbox plumbing for the single foreground agent.

## Purpose

`internal/app` runs exactly one foreground agent at a time. This
package is the seam between that TUI shell and the underlying agent loop in
[`packages/core.md`](../3-core/core.md). The shell starts a run, hands user input
to it, observes its outbox, and routes permission requests back to the user.

Subagents (parallel background agents) are owned separately by
[`packages/subagent.md`](subagent.md); cron and async triggers feed into the
same `Send` path used by user input.

## Contract

Foreground agent lifecycle. `Runtime` owns exactly one run generation plus its
permission gate. The package exposes the concrete `*Runtime`; no producer-side
service interface is required.

```go
package agent

type Runtime struct { /* internal fields */ }

func (s *Runtime) Start(params BuildParams, messages []core.Message) error
func (s *Runtime) Stop(ctx context.Context) error
func (s *Runtime) Cancel() *core.TurnFinalization
func (s *Runtime) Reset(ctx context.Context) error
func (s *Runtime) Messages() []core.Message
func (s *Runtime) SetMessages(messages []core.Message)
func (s *Runtime) Active() bool
func (s *Runtime) Send(msg core.Message) error
func (s *Runtime) Outbox() <-chan core.Event
func (s *Runtime) PermissionGate() *PermissionGate

// Package-level access
func Initialize(opts Options)
func Default() *Runtime
```


## Internals

- `runtime.go` tracks one `run` (`core.Agent`, cancel, done) plus its
  `PermissionGate`. A generation is not cleared until `Run` returns, so a
  stop and restart cannot overlap.
  The native message chain survives both deliberate stops and unexpected exits;
  resume can seed it before the first run. `Reset` clears it after shutdown.
- `build.go` translates `BuildParams` (model, identity, skills, tools,
  permission mode, cwd, ...) into a `core.Config` for `core.NewAgent`.
- `permission.go` owns the gate: a thread-safe channel pair that turns
  asynchronous permission asks into synchronous TUI approval modals.
- Runtime owns live native messages; the opened Session owns persistence and its
  writer lease. Stopping Runtime retains history. The Session remains open until
  it is explicitly closed; see [session](session.md).

## Lifecycle

- Construction: `Initialize(Options{})` runs at app startup, registering the
  singleton.
- Per-run: `Start(params, messages)` builds a `core.Agent` and launches
  its `Run` goroutine. The agent's outbox is the only return channel.
- Termination: `Stop(ctx)` cancels and waits for the exact run generation.
  The caller owns the deadline; the TUI allows two seconds. A deadline error
  retains the run and history, and prevents replacement or successful teardown.
  `Reset(ctx)` clears history only after successful shutdown, using the same context.
  `Active()` changes only after the run goroutine exits. A run's error reaches
  the app as `core.AgentStopped`.
- Turn cancellation: `Cancel()` immediately cancels and captures one exchange,
  keeping the runtime active for later input.
  Its receipt offers `ExecutionDone()` for execution-only completion and `Wait(ctx)`
  for execution plus consumer cleanup and essential message/inference/state writes.
  Neither prevents successor execution. Nil means no unfinished exchange was captured:
  `ExecutionDone()` is already closed, while `Wait(ctx)` returns an error.
  The between-turn interrupt latch is unchanged.
  `ErrTurnCompleted` denotes a natural completion race, not a save failure. TUI input
  waits asynchronously for cleanup; manual stop holds queued/background work.
  Queue-selection Enter explicitly resumes the queue head after cleanup, even with
  held harness input; direct pending user input takes precedence. Edits, deletion,
  or session replacement invalidate that deferred intent.
  No guarantee covers audit details, normal-turn hooks, rendering, or fsync.
- Normal completion: `EndTurn` records state before queued input or AutoPilot.
  `StopCheck` runs the hook; `StopDecision.Finish` records before finishing or
  continuing. Failed recording stops that boundary; unchanged state is not rewritten.
  TUI owns cleanup and stale-result checks. Cancellation/failure paths and receipt
  guarantees are unchanged; recording errors do not create a global input hold.
- Headless execution: `RunOnce` builds the same prompt, tools, and permission
  gate, then calls `ThinkAct` directly with outbox delivery disabled. Text and
  recording still use synchronous observers, so long streams cannot fill an
  unconsumed outbox.
- Sending: callers pass the `core.Message` built from the conv row so both
  share one ID. `Send` appends to the agent's message queue (never blocks) and
  reports a stopped run instead of silently dropping input.

## Tests

```
internal/agent/completion_test.go — recording/successor order and Stop-hook policy.
internal/app/completion_test.go — native state-record counts, successor ordering,
                                  changed post-hook state and recording failures.
internal/app/stop_hook_fence_test.go — delayed outcomes and exact lifetime fencing.
internal/agent/runtime_test.go — lifecycle serialization, message identity,
                                 and unexpected runner exits.
internal/agent/runtime_interrupt_test.go — inactive cancellation receipt.
internal/agent/runtime_interrupt_http_test.go — actual HTTP abort and execution unwind.
internal/core/turn_finalization_test.go — receipt identity, completion and errors.
internal/app/turn_finalization_test.go — consumer acknowledgment and state-write errors.
internal/core/run_interrupt_test.go — repeated cancellation and successor safety.
internal/app/session_resume_test.go — native error retry, inactive compaction,
                                      and image-preserving model switches.
internal/agent/runonce_stream_test.go — headless streams exceeding the default
                                       outbox capacity, with and without text
                                       callbacks, preserving event observers.
```

A unit test for `BuildParams → core.Config` translation is missing and
worth adding (logged in `notes/tech-debt.md`).

## See Also

- Code: `internal/agent/`
- Underlying primitive: [`packages/core.md`](../3-core/core.md) (the `Agent`
  interface and the inbox/outbox event model)
- Background agents: [`packages/subagent.md`](subagent.md)
- Permission model: [`concepts/permission-model.md`](../../concepts/permission-model.md)
- Layer: `feature` (see [`reference/dependency-rules.md`](../../reference/dependency-rules.md))
