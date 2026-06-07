---
package: github.com/genai-io/san/internal/app
layer: app
---

# ui

The Bubble Tea TUI shell. Composes the agent loop, conversation view,
user input, hub-routed agent events, system triggers (cron / async
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
    userInput   input.Model      // Source 1: user keyboard
    eventHub    *hub.Hub         // Source 2: agent-to-agent pub/sub
    events      chan hub.Event   // consumer-owned buffer
    systemInput trigger.Model    // Source 3: cron / async hook / file watcher
    conv        conv.Model       // agent outbox → conversation view
    env         env              // app-local TUI state
    services    services         // 16 injected feature-layer service refs
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

Each sub-model package (`conv/`, `input/`, `trigger/`, `hub/`, `kit/`)
defines its own narrow `Runtime` interface. Root implements those via
adapter methods on `*model`, never reaching down into root from a
sub-model.

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
| `model_scrollback.go` | Render committed messages into terminal scrollback via `tea.Println`. |
| `model_agent_events.go` | `conv.Runtime` callbacks (turn start, tokens, tool results, turn end, stop). |
| `model_compact.go` | Conversation compaction (auto + `/compact`). |
| `model_tool_effects.go` | Side effects from tool calls (cwd, files, agent launches, overflow). |
| `model_workspace.go` | cwd / file change reactions + FileWatcher setup. |
| `model_turn_queue.go` | Turn-end inbox drain + prompt injection + stop-hook gate. |
| `model_deps.go` | Deps builders for sub-features (`overlayDeps`, `triggerDeps`, etc.). |
| `model_actions.go` | Identity switch + slash-command dispatch from selector hotkeys. |
| `update.go` | `Update()` dispatch + `routeFeatureUpdate` + `overlaySelectors`. |
| `update_keys.go` | Keyboard handling + active-modal delegation + Ctrl+O double-tap. |
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
| `app/hub/` | Source 2 (agent → agent) | Pub/sub bus for subagent completion events. |
| `app/trigger/` | Source 3 (system) | File watcher, cron poll, async hook callback. |
| `app/kit/` | shared | Reusable TUI widgets (panel, listnav, theme, suggest, history). |

## Lifecycle

- `cmd/san` calls `app.Run()` which builds the `tea.Program`,
  `newServices()` snapshots all `Default()` references, the root model is
  constructed, and `tea.Program.Run()` enters the MVU loop.
- Per turn: user submits → input subpackage → `sendToAgent()` → agent
  inbox → agent processes → outbox events → `conv` updates → re-render.
- On `/plugin install`, `/model`, etc.: `ReloadPluginBackedState()`
  re-initializes the affected services and calls `refreshAfterReload`.

## Tests

The `app` package itself has no unit tests — coverage is exercised
end-to-end via integration tests (`tests/integration/`). Sub-model
packages have their own tests:

```
internal/app/conv/message_test.go              — message rendering.
internal/app/conv/markdown_test.go             — markdown renderer.
internal/app/conv/tracker_view_test.go         — task tracker view.
internal/app/input/on_approval_test.go         — approval flow.
internal/app/input/on_mcp_test.go              — MCP slash command.
internal/app/input/on_plugin_test.go           — plugin slash command.
internal/app/input/on_provider_test.go         — provider selector.
internal/app/input/on_queue_test.go            — input queueing.
internal/app/input/on_textarea_test.go         — textarea behavior.
```

## See Also

- Code: `internal/app/`
- End-to-end data flow (keystroke → agent → render): [`concepts/data-flow.md`](../concepts/data-flow.md)
- Rendering pipeline (View(), Markdown, tool blocks): [`concepts/rendering.md`](../concepts/rendering.md)
- Underlying primitive: [`packages/core.md`](core.md) (`Agent` interface)
- Foreground session wrapper: [`packages/agent.md`](agent.md)
- Layer: `app` — top of the stack, may import any feature package.
