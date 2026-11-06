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
    Reminder  *reminder.Reminder
}
```

Each sub-model package (`conv/`, `input/`, `trigger/`, `kit/`) defines its
own narrow `Runtime` interface. Root implements those via adapter methods
on `*model`, never reaching down into root from a sub-model. Source 2
(agent → main) is not an app sub-package: it is the process-wide
[`broker`](broker.md), which the root opens as `broker.Main`.

### Human interactions

`conv.AgentToUI` carries context-bound requests over one session-owned channel.
`interactions.go` owns a FIFO and one active request across approvals, questions
and secret prompts. A modal never replaces another unanswered request; existing
panels and native-history handoffs finish before the next prompt is shown.
The transport poll continues while the main agent is idle or a modal is open.

Each response must match the active request. Cancellation retires only its
caller; reset and shutdown close the transport logically and reject late delivery.
Worker permission metadata comes from its backend-prepared context, not the
parent's cwd or grants. Only once/reject is offered for worker approvals.
`interaction.PreparePermission` and `Permission` handle preparation and resolution
under the [shared contract](../reference/dependency-rules.md#rules). TUI supplies
option labels, grant/audit callbacks and persistence notices.
`interactions_renderer_test.go` drives real modal keys and verifies that worker
tool execution and the next inference receive the decision/answer.

### ACP stdio host

`san acp` enters `RunACP` without constructing a Bubble Tea model. Stdout is
reserved for SDK ACP frames; diagnostics use stderr. The SDK owns initialization,
request admission, session cancellation, transport limits and response barriers.
`acp.go` composes independent native sessions, settings policies, hooks, reminders
and result stores. `acp_turn.go` consumes native runtime events and translates
streaming text and tool progress. `acp_permission.go` offers only allow-once and
reject-once; unknown, cancelled and failed responses never authorize a tool.
Native deny rules and hook-approval eligibility remain authoritative.

San configures the SDK's `Limits.WriteTimeout` to 30 seconds per active frame
(excluding queue wait), ample for a local editor draining bounded 64 KiB tool
previews. This is not a per-turn deadline. A stalled update, permission request
or response shuts down the whole connection, cancels every session and joins
callbacks before closing native resources; failed delivery remains uncertain.
The CLI owns duplicates of stdin/stdout; the inherited originals must not be
used concurrently. Unix requires pipes or sockets, enables nonblocking I/O before
registering with Go's poller and verifies deadline support. `O_NONBLOCK` is shared
with the original descriptors and deliberately not restored while I/O may be
active. Windows accepts pipes: overlapped handles require Go deadline support;
synchronous inherited pipes use locked OS threads and `CancelSynchronousIo`.
Close retries cancellation across the syscall-entry race and joins the operation
before releasing its handle; there are no detached I/O pumps. Consoles and regular
files are not ACP transports. Tests cover full OS pipes, healthy framing, EOF,
interrupted I/O and short-budget host shutdown; native Windows CI runs the Windows
adapter tests (cross-compilation alone does not validate cancellation). External
editors and live vendors have not been validated.

Each ACP session owns its file-observation store across prompts. Tool contexts
carry that store; native TUI/subagent callers retain their existing default.
Native HookResponse handoffs use an owner-local consuming store, not SDK display
Details or process-global call IDs. Write/Edit therefore fire FileChanged and
populate that session's compaction file cache exactly once per handoff. Shell cwd
is fixed per session: `cd` affects only the current command, never another call.
On disconnect, the host joins runtimes, runs SessionEnd, then cancels and joins
all detached hooks (including completion audit callbacks) before closing the
transcript. In-process function hooks must honor cancellation; shutdown waits
for their cleanup rather than closing a writer under them. Regression coverage:
`acp_native_test.go`, `fs/file_view_owner_test.go`, and `hook/shutdown_test.go`.

The configured provider supplies the initial model and the session's model choices.
Each absolute session cwd loads its own trusted settings and instruction files.
In `acpSession`, `nativeSession` pairs the native transcript session and result store;
`runtimeBuildParams` retains runtime construction inputs across rebuilds.
`permissions` owns session grants and mode; its `ForDirectory` method builds
native policies with directory-scoped rules sharing that same permission state.
UserPromptSubmit, pre/post-tool hooks, automatic compaction context restoration,
Stop continuations and SessionEnd use native helpers. Terminal handling records
session state and acknowledges the native receipt before admitting a successor;
disconnect joins callbacks and runtimes before closing transcript writers.

The host supports text prompts, Read/Write/Edit/Glob/Grep, Bash, Agent, Skill,
and TaskOutput/TaskStop, subject to disabled tools and native permission rules.

- **Extensions** (`acp_extensions.go`, `acp_commands.go`): each session loads its
  own skills, plugins, agent definitions and native/client MCP servers under
  native workspace trust. Client MCP config is not saved; conflicting native
  names fail. Stdio servers use the session cwd; schemas are fixed at setup.
  Only prompt commands execute, including plugin commands; TUI management
  commands fail. Command availability is sent on prompts.
- **Load** (`acp_session.go`): `session/load` restores the native conversation
  under its writer lease and replays text/tool history. Image and opaque reasoning
  blocks are retained but not rendered. Unsupported saved providers/models fail;
  saved bypass/autopilot modes never grant those permissions.
- **Configuration** (`acp_config.go`): `session/set_mode` offers normal and
  auto-accept, preserving explicit deny/ask rules. `session/set_config_option`
  selects models within the configured provider; `session/set_model` and
  cross-provider switching are unsupported. SessionPermissions alone owns the mode;
  protocol responses and transcript patches read it directly. Changes persist before
  taking effect.
  Active prompts or background work (including cleanup) return `Session busy`
  without changing live or saved state.
- **Background work**: Bash accepts `background_job` (native deadlines) and
  `background_service` (no execution deadline). Prompt cancellation leaves launched
  tasks running. TaskOutput polls session-local status/output; TaskStop cancels
  through the normal permission gate. There are no completion pushes or automatic
  wakeups. Output is bounded in memory and not restored on load; only polled output
  enters the transcript. Disconnect cancels all tasks and waits for process reaping
  and output draining before closing hooks, MCP clients and transcripts.
- **Subagents** (`acp_agents.go`): foreground workers are prompt-bound and can
  request approval. Background workers are headless and deny unresolved approvals;
  child settings cannot override parent denies. Workers cannot launch nested agents
  or background Bash, and hook-forced approvals deny. Use TaskOutput/TaskStop;
  AgentStop, messaging and continuation are unavailable.
- **Input** (`acp_input.go`): clients advertising
  `initialize.clientCapabilities.elicitation.form` get AskUserQuestion choices
  with optional Other text, plus ordinary line-input replies for foreground Unix
  Bash (including foreground workers), through `elicitation/create`. Background
  work remains noninteractive. Declined, cancelled or malformed replies supply no
  answer; cancellation and disconnect unblock pending input. This is not a terminal
  UI. Secret prompts decline without a form: masked input and out-of-band credential
  flows are unsupported. Never enter secrets in ordinary forms.

Non-text prompts, session listing/resume-without-replay, WebFetch/WebSearch,
planning tracker, Workflow, Cron and Evolve are not offered. ACP is not a
replacement for the TUI.

Optional endpoints use the SDK version pinned in `go.mod`.

Run `go test -race ./internal/app -run '^TestACP'` for protocol, permission,
load/configuration, extension, input and background-lifecycle coverage. Tests use
scripted providers and local MCP/subprocess fixtures, not editor or live-vendor
acceptance.

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

### Image turns

`update_submit.go` resolves file references and pasted attachments on both direct
and queued sends. `input.ImageDisplay` replaces loaded file references at their
original positions with numbered markers and orders pasted and file images
by appearance. The agent-bound message uses `ChatMessage.ToMessage` to keep
that text/image order in the native transcript. Queued edits remove attachments whose tokens
were deleted, and a canceled stream restores queued token IDs without replacing
an unrelated draft's pending attachments.

`describeImages` appends source paths to the **model text** for vision and
text-only models, while keeping the UI's display text separate. Pasted pictures
are written under the session's `blobs/image/<session-id>/`, so paths remain
readable after resume or compaction; text-only requests filter out native image
blocks without changing stored history. `core.BuildCompactionText` replaces
image blocks with numbered `[Image #N]` markers at their original positions,
even for older turns without a source path or user text.
If the session blob cannot be written, the message does not claim that a
clipboard filename is a readable path. `internal/image` accepts at most 32 MiB of compressed input and 20 million
pixels. Oversized payloads or images over 2000 px on either axis are resized
or re-encoded to at most 5 MiB of binary image data; unchanged smaller images
retain their original bytes. File references retain their original source path,
even if the attached payload is resized. For an oversized animated GIF, only
the first frame survives normalization.

### Return recaps

`recap.go` prepares a recap after the terminal loses focus and shows it on
return only after the configured away time. Automatic recaps require at least
three conversation turns and, after a recap, two new turns. The last recap is
identified by its persisted `/recap` notice in the conversation, not a
process-local counter. If nothing new is due at blur, no preparation timer or
model request starts; turn end still checks for new work finished while away.
Returning again or resuming a finished session does not repeat the same work.
`/recap` remains available on demand. Tests live in `internal/app/recap_test.go`.

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
| `agent.go` | Agent session lifecycle helpers (`sendToAgent`, `ListenForAgentEvents`, `ReconfigureAgentTool`). |
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
| [`broker`](broker.md) | Source 2 (agent → main) | Process-wide message queue (not an app sub-package); root registers `broker.Main`. |
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
reserve one row above the live tail instead of hiding the conversation. The
frozen frame is captured after selecting that one-row print chunk, preserving
the existing print safety protocol. `terminalOutput` writes the temporary frame
and history insert together, keeping synchronized output open across both when
supported. Its `Read`, `Write`, `Close`, and `Fd` methods preserve Bubble Tea's
terminal-file contract; forwarding only `Fd` prevents terminal-size detection
and leaves normal interactive startup with a zero-size frame. The queue caches
parsed rows and rewraps the remainder only on width
changes. Activity indicators fit within the rows left
by the composer or docked modal; the view and overflow check share that cap.
`/clear` discards retained rows and cancels queued prints. An already-issued
print finishes before the terminal wipe, and stale print messages cannot revive
cleared output. This layout needs no configuration.

The live welcome banner uses the first message's leading newline for spacing,
matching its settled rendering. Adding another blank row to the live banner
moves the first user message upward when the first inference commits it.

Submitting an idle turn immediately shows the response spinner while waiting
for the first inference event. This waiting state is display-only; the agent's
`InferenceStart` creates the actual assistant message. Input during the wait is
queued, and cancellation or a failed send clears the spinner.
During an inference, one breathing status row reads `✦ On it · 17s`, including
while commentary, thinking, or partial tool arguments stream. It is footer
content, ordered below Tasks/background indicators and queued messages, directly
against the input's top separator without a blank row between them. Queued
prompts have no heading; `+N more` counts only hidden messages, inline on short
screens. Approval and
question panels suppress the standalone row. Genuine silence after 30 seconds
shows `Waiting for model`; actual retries show their attempt number. Tool-input
progress tracks liveness internally without displaying byte counts or fake phases.
`InferenceEnd` retires generation progress before pending tools run. A stopped
failed turn explicitly marks its output as incomplete. This status is display-only
and never enters native history or the model conversation. Regression coverage
includes `conv/inference_progress_test.go`, `model_inference_lifecycle_test.go`,
`view_inference_placement_test.go`, and `view_footer_history_test.go`.
`Session.Send` is nonblocking and runs before the submit handler returns, so an
Esc cannot be followed by a delayed command sending the cancelled request.

The footer keeps the model, reasoning effort, usage, and temporary status
messages, separated by ` · `:
`gpt-5 (high) · 32.0k/128.0k · ChatGPT 5h 75% left (2h 35m) / week 50% left (3d)`.
Reset countdowns use readable days/hours or hours/minutes, with at most two units.
It does not add a persistent history shortcut hint when a turn ends;
idle Ctrl+O and `/history` still open the transcript viewer.

While a tool-call batch is pending, the conversation tail docks above the
activity/input area, with spare rows above it. Long shell commands use the shared
lossless inset command block beneath their header. When the
description is empty, the header uses a shortened command preview; short
commands that fit stay on the header. This reduces the transient content xterm
can push into native history during a narrowing and shortening resize. Tool
headers reserve width for running detail, optional agent hints appear only when
they fit, and expanded tool content wraps explicitly to the terminal width.
Wrapping command bodies and string parameters preserves spaces, including
spaces inside quoted arguments. Bash's configured output preview rolls over the
last N actual output lines while running. The `$` prompt, faint `│` output guide,
and closing `└` align with the tool-name text, using only the two-column outer
inset without extra marker padding. Command and output text share the next
content column, and omission markers belong inside that guide: `│ … (28)`
means 28 whole output lines were omitted. Counts come from exact SDK progress
metadata, never a rounded counter or the byte-capped tail alone. When the total
is unknown or only part of a long row was cut, show `│ …` without guessing a
count. The omission row is outside the N-line budget. Settled successes close
with `└` on their last output row; running output stays open, and failures close
on `└ failed · reason`. There is no extra line-count footer. Trailing blanks are
real output lines, and expanded output/logs retain the full evidence.

Completed Agent blocks show the configured tail of worker prose; routing metadata
such as Task ID does not count toward preview rows. Expansion keeps the full result.
Older records use the legacy preview.
Expanded shell calls use the same `$` command block; other expanded parameters
keep continuation lines aligned with their values.

When foreground workers overflow the live tail, `conv.RenderActiveWorkers`
reserves their identity rows and compact rows for ordinary pending tool siblings
before sharing recent details. The latest notice also keeps a row. Background
tracker/workflow indicators spend only the height left after this reservation,
so a long workflow cannot hide every foreground worker. Clipped indicator blocks
keep their leading newline; they never merge onto a tool's identity row. Space
shortages show explicit tool/worker or detail omission counts. The composer and
docked modal retain priority over all activity.

Worker activity travels through per-call bounded buffers in `AgentToUI` and is
applied as one poll batch. Model/mode/usage metadata is retained separately from
recent tools; metadata is the worker's, not a fallback from the parent. Background
progress shares the existing tracker row instead of adding a second row.
Shell progress uses SDK `Report`/`ToolUpdate.Partial.Details` with `BashDetails`
for exact line counts, byte-tail truncation, and recent output. It replaces the
pending call's display snapshot without restarting its timer, adding transcript
content, or accepting late reports for completed/foreign calls. Agent activity
continues using the separate bounded bridge.

The task tracker above the input uses one shared `Tasks` heading and shows at
most eight task rows and one overflow summary. The heading appears only while
the tracker is visible, and shows progress only when plan items are present.
Background rows lead with a status icon and work description, without repeated
executor-kind labels. Dim elapsed time follows a middle dot. Long descriptions
truncate to leave room for elapsed time or an
exception status. Terminal background tasks leave their individual tracker rows after their
completion notice enters the conversation. Notices appear immediately during
inference. Each inference binds to a message ID at `InferenceStart`; stream
updates, rendering, and scrollback commits use that identity regardless of
display order. `InferenceEnd` releases the identity after final metadata arrives;
cancellation clears it immediately, and retries bind a new message. Turn end
cannot reset a worker before its notice arrives.
Completion notices and output records remain available. Resumed completed
records whose workers are absent have no live notice to wait for. Announced
failed, stopped, killed, and interrupted workers share one muted, non-actionable
`History · N · /tasks` entry below current task identities. Stops are not labelled
as failures, and detailed status categories belong in the expandable task viewer,
not a combined status-word list beside the input. History shares the overflow
summary row rather than consuming any of the eight identity slots. History alone
needs no extra `Tasks` heading. Success-only history leaves no summary. Until
announcement, terminal rows retain their status and final reason. Folding never
changes task records or notices.
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
  reloads the effective setting. See [configuration](../reference/configuration.md).
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

## Copilot quota status

`copilot_quota.go` shows account quota in the shared subscription status slot:
`gpt-4.1 · 12.0k/128.0k · Copilot Chat 75% left (28d 19h)`. Available finite
premium quota takes precedence over Chat. Date-only resets display `(Dec 1)`.

Fetching is asynchronous, Copilot-only, and cached for two minutes; no settings
are required. The label hides on narrow terminals. Tests in
`copilot_quota_test.go` cover formatting, refresh, and status integration.
See [Copilot account quota](llm.md#copilot-account-quota) for API semantics.

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
internal/app/view_observability_test.go        — worker identities, modal height, notices and progress.
internal/app/view_worker_budget_test.go        — mixed tools and competing workflow height budgets.
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
- End-to-end data flow (keystroke → agent → render): [`concepts/data-flow.md`](../concepts/data-flow.md)
- Rendering pipeline (View(), Markdown, tool blocks): [`concepts/rendering.md`](../concepts/rendering.md)
- Underlying primitive: [`packages/core.md`](core.md) (`Agent` interface)
- Foreground session wrapper: [`packages/agent.md`](agent.md)
- Layer: `app` — top of the stack, may import any feature package.
