---
package: github.com/genai-io/san/internal/app
layer: app
---

# ui

The Bubble Tea TUI shell. Composes the agent loop, conversation view,
user input, broker-routed main-loop notices, system triggers (cron / async
hook / file watcher), and runtime services into a single `tea.Model`.

## Purpose

This is the only `app`-layer package: everything user-facing lives here.
It owns nothing domain-specific — every business behavior comes from a
`feature`-layer service injected through the `services` struct. The
package's job is **composition and event routing**, not behavior.

## Contract

There is no single `Service` interface — `app` is the top of the layer
stack and nothing else imports it. Instead the contract is the
**sub-model `Runtime` interfaces** that each subpackage exposes upward
to the root model. The root implements them through adapter methods.

```go
package app

// model is the Bubble Tea root model. One per process.
type model struct {
    userInput       input.Model        // Source 1: user keyboard
    mainNotices     chan mainNotice    // Source 2: notices bound for the main loop
    systemInput     trigger.Model      // Source 3: cron / async hook / file watcher
    conv            conv.Model         // agent outbox → conversation view
    env             env                // app-local TUI state
    services        services           // 16 injected feature-layer service refs
}

// services holds references to feature-layer service singletons.
// See internal/app/services.go for the full list and per-field source.
type services struct {
    Setting   setting.Service
    LLM       llm.Service
    Tool      tool.Service
    Hook      hook.Service
    Session   session.Service
    Skill     skill.Service
    Subagent  subagent.Service
    Command   command.Service
    Task      task.Service
    Tracker   tracker.Service
    Cron      cron.Service
    MCP       mcp.Service
    Plugin    plugin.Service
    Agent     agent.Service
    Identity  *identity.Registry
    Reminder  *reminder.Service
}
```

Each sub-model package (`conv/`, `input/`, `trigger/`, `kit/`) defines its
own narrow `Runtime` interface. Root implements those via adapter methods
on `*model`, never reaching down into root from a sub-model. Source 2
(agent → main) is not an app sub-package: it is the process-wide
[`broker`](../2-feature/broker.md), which the root opens as `broker.Main`.

### Known Violations

- **`services` snapshots 16 singletons via `Default()` at construction.**
  The whole codebase's singleton problem manifests here. The right shape
  is for `cmd/san` to construct each service explicitly and pass it in,
  inverting the current pull-from-`Default()` model.
- **Two `Default()` shapes coexist:** most services panic if not
  initialized; `Hook` uses `DefaultIfInit()` (nil-tolerant). Two contracts
  for one job — converge once construction moves to `cmd/`.
- **`refreshAfterReload` re-snapshots 6 of the 16 services.** Implies
  those services are *replaced* on plugin reload (their `Initialize`
  builds a new instance and stores it in their singleton). Construction
  injection would let reload edit the `services` struct in place
  instead.

## Internals

Root files (no business logic; pure glue):

| File | Role |
|---|---|
| `model.go` | Root `model` struct + `Init()`. Behaviour split across siblings. |
| `model_lifecycle.go` | Construction + run-option application + task lifecycle wiring + SessionEnd shutdown. |
| `model_session.go` | Session save/load + per-session task storage + fork. |
| `model_scrollback.go` | Retain recent settled rows above the composer; print older rows into native scrollback via `tea.Println`. |
| `model_agent_events.go` | `conv.Runtime` callbacks (turn start, tokens, tool results, turn end, stop). |
| `model_compact.go` | Conversation compaction (auto + `/compact`). |
| `model_tool_effects.go` | Side effects from tool calls (cwd, files, agent launches, overflow). |
| `model_workspace.go` | cwd / file change reactions + FileWatcher setup. |
| `model_git_bar.go` | Optional asynchronous Git snapshot polling, cancellation and footer adapter. |
| `model_turn_queue.go` | Notice delivery, turn-end queue drain + prompt injection + stop-hook gate. |
| `model_deps.go` | Deps builders for sub-features (`overlayDeps`, `triggerDeps`, etc.). |
| `model_actions.go` | Identity switch + slash-command dispatch from selector hotkeys. |
| `update.go` | `Update()` dispatch + `routeFeatureUpdate` + `overlaySelectors`. |
| `update_keys.go` | Keyboard handling + active-modal delegation + Ctrl+O double-tap. |
| `model_history.go` | Asynchronous session-history loading and existing message/tool renderers for the transcript viewer. |
| `update_resize.go` | Window resize + scrollback reflow. |
| `update_submit.go` | Submit + provider turn + skill invocation. |
| `update_command.go` | Slash command deps + execution. |
| `update_modal.go` | Operation-mode cycle + question-modal protocol. |
| `update_approval.go` | Permission approval flow + bridge response. |
| `update_input_effects.go` | Stream cancel, tool-call cancel, image paste, quit. |
| `view.go` | `View()` — composes sub-model `View()` strings into terminal layout. |
| `agent.go` | Agent session lifecycle helpers (`sendToAgent`, `ContinueOutbox`, `ReconfigureAgentTool`). |
| `services.go` | The `services` struct + `newServices()` + `refreshAfterReload()`. |
| `env.go` | `env` — app-local TUI state (provider snapshot, permissions, plan, cache). Pure state holder. |
| `hooks.go` | Hook integration glue (LLM completer wiring). |
| `init.go` | Global infrastructure init, plugin/mcp adapter wiring. |
| `run.go` | `Run()` — `tea.Program` entrypoint. |

Sub-model packages:

| Package | Source | Role |
|---|---|---|
| `app/input/` | Source 1 (user keyboard) | Textarea, selectors, approval modals, slash command dispatch. Big surface (37 files, `on_*.go` per component). |
| `app/conv/` | agent outbox | Conversation render state, streaming, message rendering, tool-call rendering, progress trackers. |
| [`broker`](../2-feature/broker.md) | Source 2 (agent → main) | Process-wide message queue (not an app sub-package); root registers `broker.Main`. |
| `app/trigger/` | Source 3 (system) | File watcher, cron poll, async hook callback. |
| `app/kit/` | shared | Reusable TUI widgets (panel, listnav, theme, suggest, history). |

The normal view fills the terminal height and anchors the composer and status
line to the bottom. Matching neutral rules frame the input area. Short
conversations grow downward from the top, with spare space between the
conversation and the docked activity/input area. Multiline
input grows upward; command and file candidates
appear above its separator. Recent settled output remains in a bounded row
buffer, with older output entering the serialized native print queue. Every
`Update` checks the space left by live output and bottom controls, so settled
rows reach history before being cropped. The buffer keeps unwrapped source
lines; resize rewraps them and prints any overflow. Full-height history inserts
temporarily use the composer footer as their frozen frame, preserving the
existing print safety protocol. Activity indicators fit within the rows left
by the composer or docked modal; the view and overflow check share that cap.
`/clear` discards retained rows and cancels queued prints. An already-issued
print finishes before the terminal wipe, and stale print messages cannot revive
cleared output. This layout needs no configuration.

The footer keeps the model, reasoning effort, usage, and temporary status
messages. It does not add a persistent history shortcut hint when a turn ends;
idle Ctrl+O and `/history` still open the transcript viewer.

While a tool-call batch is pending, the conversation tail docks above the
activity/input area, with spare rows above it. Long shell commands show only
their header and elapsed/progress detail until expanded or completed. When the
description is empty, the header uses a shortened command preview; short
commands that fit stay on the header. This reduces the transient content xterm
can push into native history during a narrowing and shortening resize. Tool
headers reserve width for running detail, optional agent hints appear only when
they fit, and expanded tool content wraps explicitly to the terminal width.
Wrapping command bodies and string parameters preserves spaces, including
spaces inside quoted arguments.

The task tracker above the input uses one shared `Tasks` heading and shows at
most eight task rows and one overflow summary. The heading appears only while
the tracker is visible, and shows progress only when plan items are present.
Background rows lead with a status icon and work description, without repeated
executor-kind labels. Dim elapsed time follows a middle dot. Long descriptions
truncate to leave room for elapsed time or an
exception status. Successful background tasks leave the tracker after their
completion notice enters the conversation. Notices appear immediately during
inference. Each inference binds to a message ID at `MessageStart`; stream
updates, rendering, and scrollback commits use that identity regardless of
display order. `MessageEnd` releases the identity after final metadata arrives;
cancellation clears it immediately, and retries bind a new message. Turn end
cannot reset a worker before its notice arrives.
Completion notices and output records remain available. Resumed completed
records whose workers are absent have no live notice to wait for. Failed,
stopped, or interrupted work stays visible when idle.
Plan items keep their IDs and `Tasks` progress heading, and a finished plan hides
when the main agent becomes idle. In-progress items take priority, followed by
failed or interrupted items, pending items, and completed plan items. Within
each group the newest items stay visible, while displayed rows retain their
original order. Overflow counts distinguish in-progress, failed/stopped,
pending, and completed plan items; rendering leaves task records intact.

### Optional Git information bar

- **Configuration:** `/settings` → Appearance → STATUS BAR groups Context usage
  and Git information in compact Off / On controls. Left / Right previews; Enter
  saves the focused preference. `gitBar` defaults off; `input.GitBarSavedMsg`
  reloads the effective setting. See [configuration](../../reference/configuration.md).
- **Flow:** `model_git_bar.go` reads through `proc.ReadGitStatus` in a bounded
  `tea.Cmd`; `conv.RenderGitBar` draws a plain-text snapshot beneath the normal
  status line. `renderFooter` accounts for its height and composer cursor.
- **Lifecycle:** startup, enabling and cwd changes read immediately, then poll
  after three seconds. Disabling or changing cwd cancels the read; generation
  tags reject stale results and ticks. Service reloads rebind Settings panels
  without closing the popup or changing its tab.
- **Layout/pitfalls:** both toggles fit on initial open at 80×24; short panels
  window around recorded cursor/section rows and pin save errors. Never run Git
  from `View` or synchronously in `Update`, expose remote credentials, or treat
  an unavailable snapshot as clean.

## Lifecycle

- `cmd/san` calls `app.Run()` which builds the `tea.Program`,
  `newServices()` snapshots all `Default()` references, the root model is
  constructed, and `tea.Program.Run()` enters the MVU loop.
- Per turn: user submits → input subpackage → `sendToAgent()` →
  `core.Agent.Append` → agent processes → outbox events → `conv` updates → re-render.
- Before a deliberate foreground-agent stop, the root snapshots the live
  `core.Agent` message chain. A replacement agent is seeded from that snapshot
  rather than the UI rendering model; `/clear` and loading another persisted
  session explicitly discard it.
- On `/plugin install`, `/models`, etc.: `ReloadPluginBackedState()`
  re-initializes the affected services and calls `refreshAfterReload`.

## Tests

The root package has focused lifecycle/state tests and broader coverage through
`tests/integration/`. Sub-model packages keep their own tests:

```
internal/app/conv/message_test.go              — message rendering.
internal/app/conv/markdown_test.go             — markdown renderer.
internal/app/conv/tracker_view_test.go         — task tracker view.
internal/app/conv/git_bar_test.go              — Git row content, CJK widths and truncation.
internal/app/model_git_bar_test.go             — polling, stale events, settings and footer height.
internal/app/input/config_git_bar_test.go      — Appearance toggle, persistence and short panels.
internal/app/view_test.go                     — fixed composer, multiline input and candidates.
internal/app/model_scrollback_test.go          — recent output retention and resize.
internal/app/model_scrollback_renderer_test.go — native history integrity through the real renderer.
internal/app/agent_restart_test.go             — message-chain integrity across stop/restart.
internal/app/input/on_approval_test.go         — approval flow.
internal/app/input/on_mcp_test.go              — MCP slash command.
internal/app/input/on_plugin_test.go           — plugin slash command.
internal/app/input/on_provider_test.go         — provider selector.
internal/app/input/on_queue_test.go            — input queueing.
internal/app/input/on_textarea_test.go         — textarea behavior.
```

## See Also

- Code: `internal/app/`
- End-to-end data flow (keystroke → agent → render): [`concepts/data-flow.md`](../../concepts/data-flow.md)
- Rendering pipeline (View(), Markdown, tool blocks): [`concepts/rendering.md`](../../concepts/rendering.md)
- Underlying primitive: [`packages/core.md`](../3-core/core.md) (`Agent` interface)
- Foreground session wrapper: [`packages/agent.md`](../2-feature/agent.md)
- Layer: `app` — top of the stack, may import any feature package.
