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
[`packages/core.md`](core.md). The shell starts a run, hands user input
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
  permission mode, cwd, ...) into a `core.Config` for `core.NewAgent`. Root gates
  stamp the tool-call ID before hooks, reviewer and human approval run, matching
  subagent gates (tool execution wrappers alone run too late to identify approval).
  `FileViews` binds the conversation's observed file versions to tool and
  permission contexts for read-before-write checks; it is distinct from the
  compaction `FileCache`. Hosts retain it across runtime restarts.
  `ToolResponse(callID, response)` hands native tool-specific hook
  payloads to the host synchronously before ToolEnd. Hosts provide concurrency-safe
  storage and consume each response during PostTool; ACP owns a map per session
  because call IDs can collide across sessions. Nil keeps the TUI defaults for
  both dependencies. These are runtime wiring, not `MainSession` persistence.
  `Skills`/`Plugins` bind host-owned registries to tool/gate contexts and subprocess
  environments; nil uses TUI defaults. Hosts manage their hooks and MCP lifetimes.
  `BashDirHelp` is only a schema sentence override, with empty preserving TUI help.
  ACP supplies command-local directory wording; actual directory behavior remains
  with `CWD`/`CWDFunc` and the host's handling (or ignoring) of directory handoffs.
- `main_session.go` pairs only the opened `Session` and full-result `Store`.
  `MainSession.Validate` rejects missing/closed owners and mismatched stores without
  mutating services. Native session APIs provide recording; `ResultPager` captures
  the validated store and session ID. `internal/app.buildAgentParams` explicitly
  binds recording, permission policy, hook/skill audits and live review/recall to
  `BuildParams`, the sole runtime configuration. Stop extension work before rebinding
  its session callbacks; the builder does not own extension lifetimes.
- `result_pager.go` owns the main-agent output policy: text above 100,000 bytes is
  persisted under the captured session owner, with a UTF-8-safe 10,000-byte preview.
  Failed persistence explicitly says the full content was not saved. Result metadata
  and the existing blob handoff format are unchanged; recall is attached after paging.
  These native components are not an ACP endpoint or a complete headless host:
  callers still drain events, handle human permissions, manage recall/background work,
  record application state and stop/close their resources. Successful runtime stop
  does not prove persistence; inspect the recorder and honor turn finalization.
- `permission.go` owns the gate: a thread-safe channel pair that turns
  asynchronous permission asks into synchronous TUI approval modals.
- `permission_policy.go` supplies `PermissionPolicy.Decide`, `Review` and
  `CanApproveByHook` to native `BuildParams`. TUI and print share settings rules and
  deny-aware auto-memory maintenance. TUI supplies a live reviewer/config snapshot;
  the policy enforces safety guards, a 30-second review budget, fail-closed human
  escalation, exact session grants and best-effort decision audits before return.
  Cancelled reviews cannot cache late approvals. The session grant owner and audit
  callback are bound at construction; callers stop that runtime before replacing
  its session. Human prompts remain in `interaction`, rule precedence in `setting`,
  and judge inference in `reviewer`; this is not full headless runtime assembly.
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
- Compaction: `CompactRequest.Run` owns manual PreCompact context and summarization
  on the caller's context and selected client. `RecordManualCompaction` records an
  inactive conversation's native summary/boundary before the caller replaces its
  chain; live replacement still belongs to the SDK loop. `PostCompact` schedules
  the hook, refreshes memory, resets reminders and restores recent files only for
  manual compaction. TUI retains provider adaptation, admission, projection and
  queue release. Accepted manual compaction invalidates pending Stop decisions,
  even if summarization later fails; overlapping manual requests are rejected.
  The app's `pendingCompaction` owns a cancellable request context and captures
  the opened recording owner, session ID and runtime outbox. Results carry that
  exact context identity and are checked before any UI, history or queue effect.
  Runtime stop/reset/start, session replacement and user cancellation retire the
  request; Escape/empty Ctrl+C keep their existing overlay/draft precedence and
  hold queued input until explicit continuation. Automatic compaction remains
  runtime-owned. Cancellation reaches PreCompact and the summary HTTP request;
  it does not guarantee remote vendor cancellation or billing rollback.
- Headless execution: `RunOnce` builds the same prompt, tools, and permission
  gate, then calls `ThinkAct` directly with outbox delivery disabled. Text and
  recording still use synchronous observers, so long streams cannot fill an
  unconsumed outbox.
- Sending: callers pass the `core.Message` built from the conv row so both
  share one ID. `Send` appends to the agent's message queue (never blocks) and
  reports a stopped run instead of silently dropping input.

## Admission boundaries

`core.Config` exposes optional `InferenceAdmission`, `ToolAdmission`, and
`OutputAdmission` callbacks using native `sdkagent` payloads. They are separate
from permission `Gate` and telemetry `OnEvent`: inference admission sees the
prepared request before dispatch; tool admission sees allowed, rewritten calls
before execution; output admission sees post-filter output with its stable message
ID before history insertion and `MessageAdded`. Callback errors terminate the
exchange without retry or recovery. Already-started tools are not rolled back,
and stream fragments/start/end notifications may precede admission; telemetry is
not proof of dispatch or committed output.

Nil callbacks preserve the SDK's default path and costs. These are core-only
extension points, not foreground runtime or persistence wiring. They do not cover
input acceptance, history replacement/replay, or compaction boundaries.

## Tests

```
internal/app/main_session_test.go — TUI composition, native permission/hook/skill audits,
                                   allow/deny paging and host callback preservation.
internal/agent/main_session_test.go — production main-session/runtime tool turns, allow/deny, hook audits,
                              transcript-before-terminal ordering, live recall and cancelled review.
internal/agent/result_pager_test.go — threshold, UTF-8 preview, metadata and persistence failure.
internal/agent/permission_policy_test.go — rule/reviewer/hook composition, exact grants,
                                          audit ordering and cancelled verdicts.
internal/agent/permission_memory_test.go — memory containment and deny precedence.
internal/agent/compaction_test.go — hook/summary context, failures, replay and reminder reset.
internal/app/compact_stop_test.go — delayed Stop decisions across manual compaction.
internal/app/compact_lifetime_test.go — HTTP/hook cancellation, stale request fencing,
                                        writer replacement and input precedence.
internal/agent/completion_test.go — recording/successor order and Stop-hook policy.
internal/app/completion_test.go — native state-record counts, successor ordering,
                                  changed post-hook state and recording failures.
internal/app/stop_hook_fence_test.go — delayed outcomes and exact lifetime fencing.
internal/agent/runtime_test.go — lifecycle serialization, message identity,
                                 and unexpected runner exits.
internal/agent/runtime_interrupt_test.go — inactive cancellation receipt.
internal/agent/runtime_interrupt_http_test.go — actual HTTP abort and execution unwind.
internal/core/admission_test.go — post-hook admission before dispatch/publication,
                                  fatal failures, stable message IDs and nil defaults.
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
- Underlying primitive: [`packages/core.md`](core.md) (the `Agent`
  interface and the inbox/outbox event model)
- Background agents: [`packages/subagent.md`](subagent.md)
- Permission model: [`concepts/permission-model.md`](../concepts/permission-model.md)
- Layer: `feature` (see [`reference/dependency-rules.md`](../reference/dependency-rules.md))
