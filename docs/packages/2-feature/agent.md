---
package: github.com/genai-io/san/internal/agent
layer: feature
---

# agent

Owns the **main agent session lifecycle** — construction, start, stop, and
TUI-facing send/permission/outbox plumbing for the single foreground agent.

## Purpose

`internal/app` runs exactly one foreground agent session at a time. This
package is the seam between that TUI shell and the underlying agent loop in
[`packages/core.md`](../3-core/core.md). The shell starts a session, hands user input
to it, observes its outbox, and routes permission requests back to the user.

Subagents (parallel background agents) are owned separately by
[`packages/subagent.md`](subagent.md); cron and async triggers feed into the
same `Send` path used by user input.

## Contract

Foreground agent lifecycle. `Session` owns exactly one run generation plus its
permission gate. The package exposes the concrete `*Session`; no producer-side
service interface is required.

```go
package agent

type Session struct { /* internal fields */ }

func (s *Session) Start(params BuildParams, messages []core.Message) error
func (s *Session) Stop()
func (s *Session) StopContext(ctx context.Context) error
func (s *Session) Active() bool
func (s *Session) Send(msg core.Message) error
func (s *Session) Outbox() <-chan core.Event
func (s *Session) PermissionGate() *PermissionGate

// Package-level access
func Initialize(opts Options)
func Default() *Session
func SetDefaultSession(s *Session) // test-only
func ResetDefaultSession()         // test-only
```


## Internals

- `session.go` tracks one `sessionRun` (`core.Agent`, cancel, done) plus its
  `PermissionGate`. A generation is not cleared until `Run` returns, so a
  stop and restart cannot overlap.
- `build.go` translates `BuildParams` (model, identity, skills, tools,
  permission mode, cwd, ...) into a `core.Config` for `core.NewAgent`.
- `permission.go` owns the gate: a thread-safe channel pair that turns
  asynchronous permission asks into synchronous TUI approval modals.
- No persistence here — session/transcript state lives in
  [`packages/session.md`](session.md).

## Lifecycle

- Construction: `Initialize(Options{})` runs at app startup, registering the
  singleton.
- Per-session: `Start(params, messages)` builds a `core.Agent` and launches
  its `Run` goroutine. The agent's outbox is the only return channel.
- Termination: `StopContext` cancels and waits for the exact run generation;
  `Stop` applies a bounded default deadline and logs a run that outlives it.
  `Active()` changes only after the run goroutine exits. A run's error reaches
  the app as `core.AgentStopped`.
- Sending: callers pass the `core.Message` built from the conv row so both
  share one ID. `Send` selects over the inbox and run completion, and reports
  delivery failures instead of silently dropping input.

## Tests

```
internal/agent/session_test.go — lifecycle serialization, message identity,
                                 and unexpected runner exits.
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
