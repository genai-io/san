# Agent Communication

What a subagent is, and the one small mechanism agents use to talk.

## The contract

A subagent is: **hand a fresh, specialized agent a bounded task; get its
result back.** Two ways to run one:

- **Foreground** — run it now, block, get the result as the tool result.
- **Background** (`run_in_background`) — run several at once and be told when
  each finishes. This is where parallelism comes from.

Spawning is flat: only the main conversation spawns subagents. The `Agent`
tool is parent-only (excluded from every subagent's tool set), so a subagent
can't spawn another — nothing to enforce at runtime, the tool simply isn't
there.

## The broker

Everything agents say to each other flows through one small router —
[`internal/broker`](../packages/2-feature/broker.md):

- Every agent **registers** an address when it starts — the main conversation
  under `"main"`, a background subagent under its task id — and **unregisters**
  when it stops.
- Anyone **Sends** a `Message` stamped with a recipient. The broker routes it
  to whoever holds that address; a message to an address no one holds is
  dropped, like a call to a number no longer in service.

That is the whole model — no addresses type, no broadcast, no expiry. Two
kinds of message travel over it:

| Message | Sender → Recipient | Trigger |
|---|---|---|
| completion | a finishing background subagent → `main` | automatic, when the subagent's run ends |
| interim message | main → a running subagent, or a subagent → `main` | the `SendMessage` tool |

A delivered message arrives wrapped in `<agent-message from="…">…</agent-message>`
so the recipient can tell a routed message from real user input. Where it lands
differs by recipient — see the next section.

## Receiving: a subagent is direct, main takes one hop

Both the main conversation and a subagent are a `core.Agent` with a real
`Inbox()`, and both hand the broker a delivery function via
`Register(addr, deliver)` — that is the uniform part. What the delivery
function *does* differs, and the difference between the two endpoints is
isolated there:

- **Subagent** (headless): `deliver` pushes straight into its `core.Agent`
  inbox; the run reads it at its next step boundary.
- **Main** (UI-attached): `deliver` pushes onto `mainNotices`, the TUI's
  Source-2 channel — *not* the main agent's inbox. A human is watching, so the
  message must first surface as a notice and wait for a turn boundary; the
  Update loop (`onMainNotice`) then shows the notice and, when there is content,
  forwards it via `SubmitToAgent` into the main agent's real inbox. One extra
  hop, because main is on screen.

So `mainNotices` is a TUI input source — a sibling of the keyboard and system
triggers, not an inbox and not a broker→agent pipe. It carries
`mainNotice{Display, Content}`: `Display` is the one-line notice shown to the
user, `Content` (optional) is submitted to the agent as a turn.

```
subagent:   broker.Send ─► deliver ─────────────────────────────► its core.Agent inbox
main:       broker.Send ─► deliver ─► mainNotices ─► (turn boundary: notice
                                                       + SubmitToAgent) ─► main's core.Agent inbox
```

## Best-effort, and why the final result is separate

`SendMessage` delivery is best-effort: a subagent that has already finished —
or never takes another step — will not see the message. That is fine for what
it is used for (steering a running subagent, or a quick interim note), but it
means **a subagent's actual result must not travel by `SendMessage`.** The
result comes back on its own: foreground as the tool result, background as the
automatic completion the main loop injects at its next turn boundary.

```
main conversation ── subagent A          (foreground: result = tool result)
                 ── subagent B (task b1) ─┐ completion → the "main" address
                 ── subagent C (task c1) ─┘ (main can SendMessage b1/c1 while they run)
```

## Push, not poll

Completion is event-driven: a background run's goroutine finishes → the task
lifecycle handler sends the result to the "main" address → the main loop drains
it at the next turn boundary. The main agent never polls `TaskOutput` to learn
a subagent finished; `TaskOutput` is for inspecting output on demand.

## What deliberately does not flow

- **Conversation history** — a subagent starts from its charter and the prompt
  it is handed; the parent summarizes context into the prompt.
- **User memory** — scoped to the main loop. Project instructions
  (CLAUDE.md/AGENTS.md conventions) do flow, but only to subagents whose mode
  can mutate the workspace.
- **A finished subagent** — there is no resume. A subagent runs once; to
  continue a line of work, spawn a fresh one with the context in its prompt.

## Implementation Pointers

- The broker: `internal/broker/` (`Register` / `Unregister` / `Send`).
- Send tool: `internal/tool/agent/sendmessage.go`.
- Subagent registers its address for the run: `internal/subagent/executor_run.go`.
- Main registers its address + sends completions: `internal/app/model_lifecycle.go`,
  `internal/app/notify.go`; drains at turn boundaries in
  `internal/app/model_turn_queue.go`.

## See Also

- [`broker`](../packages/2-feature/broker.md) — the registry itself.
- [`subagent`](../packages/2-feature/subagent.md) — registry + executor.
- [`permission-model`](permission-model.md) — the gate every tool call passes.
