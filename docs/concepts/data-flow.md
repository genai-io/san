# Data Flow: Input → Agent → Render

> 中文版本：[`data-flow.zh.md`](data-flow.zh.md)
>
> Companion: [`rendering.md`](rendering.md) — how the rendered output
> is actually composed (View() layout, Markdown pipeline, tool blocks).

How a keystroke (or a cron fire, or a hub event) travels through the TUI
and becomes either a slash-command result or an agent response that the
user sees in their terminal.

## Cast

The TUI is a [Bubble Tea](https://github.com/charmbracelet/bubbletea) MVU
loop. Three Bubble Tea primitives drive everything:

- **`tea.Msg`** — an event entering the model (key press, window resize,
  spinner tick, custom in-process message).
- **`Update(msg)`** — pure function that mutates the model and returns a
  `tea.Cmd`.
- **`tea.Cmd`** — a function the framework will run; its return value is
  injected back as a new `tea.Msg`. **This is how async work re-enters the
  model.** Whenever you see a function "return a cmd," that cmd will be
  scheduled by Bubble Tea, its output captured as a `tea.Msg`, and fed
  back into `Update`.

Convention: many internal handlers return `(tea.Cmd, bool)`. The bool
means **"did I claim this event?"** — `true` stops the chain, `false`
lets the caller try the next layer. `(nil, false)` is the common
"not for me" return.

Input sources land as `tea.Msg`. **`SubmitToAgent`** is the single exit
to the running agent. Rendering happens via `tea.Println` (terminal
scrollback) plus `View()` (bottom UI strip).

```
   ┌──────────────────────────────────────────────────────────────┐
   │  Inputs                                                      │
   │                                                              │
   │   keyboard      slash command     cron       async hook      │
   │     │                │             │             │           │
   │     ▼                ▼             ▼             ▼           │
   │  handleSubmit  → SlashController  inject*    inject*         │
   │     │                │             │             │           │
   │     └────────────────┼─────────────┴─────────────┘           │
   │                      ▼                                       │
   │               SubmitToAgent(content, images)                 │
   │                      │                                       │
   │                      ▼ agent.Send (push to inbox)            │
   └──────────────────────┼───────────────────────────────────────┘
                          │
   ┌──────────────────────┼───────────────────────────────────────┐
   │  Agent loop          ▼                                       │
   │           ┌─────────────────────┐                            │
   │           │  Inbox → LLM → Tool │   ← runs in goroutine     │
   │           │     ↘    ↙          │                            │
   │           │     Outbox          │ → core.Event stream        │
   │           └─────────────────────┘                            │
   └──────────────────────┼───────────────────────────────────────┘
                          │
   ┌──────────────────────┼───────────────────────────────────────┐
   │  Render              ▼                                       │
   │             ContinueOutbox tea.Cmd                           │
   │                      │                                       │
   │                      ▼ tea.Msg                               │
   │                  Update → conv.Update → callbacks            │
   │                      │                                       │
   │                      ▼                                       │
   │             CommitMessages → tea.Println → scrollback        │
   │                      │                                       │
   │                      ▼                                       │
   │                   View() → bottom UI strip                   │
   └──────────────────────────────────────────────────────────────┘
```

## Path A — Text input

User types `hello`, presses **Enter**.

```
tea.KeyMsg('h')                  ── per keystroke
   │
   ▼
Update                            update.go
   │
   ├─ case tea.KeyMsg → routeKeypress
   │     │
   │     ├─ tryActivePopup           — question modal, approval modal,
   │     │                             or one of the slash-command
   │     │                             pickers (/model, /tools, ...)
   │     │                             nothing active for typing 'h'
   │     │
   │     ├─ HandleImageSelectKey     — image picker mode (off)
   │     ├─ HandleSuggestionKey      — prompt-suggestion mode (off)
   │     ├─ HandleQueueSelectKey     — queue-navigation mode (off)
   │     │
   │     └─ handleTextareaShortcut   — Ctrl-shortcuts / Tab / Enter / ...
   │           └─ no match for KeyRunes('h') → (nil, false)
   │
   ├─ routeToSubModel                — no sub-model claims a KeyRunes msg
   │
   └─ updateTextarea                  — textarea consumes the rune
   ▼
View                              view.go      bottom UI shows "h▮"
```

The dispatch in `routeKeypress` is a **priority stack**: a popup that is
shown (e.g. the model picker after `/model`) gets first refusal on every
key; only if nothing higher up claims the key does it reach the textarea
shortcuts, and only then the textarea itself.

Five rune-keystrokes later, textarea holds `hello`. User presses **Enter**:

```
tea.KeyMsg(Enter)
   │
   ▼
routeKeypress → handleTextareaShortcut
   │   "shortcut" = keys with a special meaning to the textarea
   │   (Ctrl-C/D/L/E/O/U/V/Y/T, Tab, Shift+Tab, Enter, Esc, ↑↓ history)
   └─ case tea.KeyEnter → m.handleSubmit()       update_submit.go
        │
        ▼
   handleSubmit
        Step 1: read textarea ────► "hello"
        Step 2: stream active? ───► no (no answer mid-stream)
        Step 3: → dispatchSubmission("hello")
                  │
                  ▼
   dispatchSubmission
        Step 1: "exit" literal? ──► no
        Step 2: prompt hook ──────► allowed
                    │  Any UserPromptSubmit hook (see hook pkg) gets to
                    │  read the prompt and optionally block it (e.g. a
                    │  policy hook rejecting prompts containing secrets).
                    │  "Allowed" = no hook blocked.
        Step 3: record to history (↑/↓ recall in the textarea)
        Step 4: slash command? ───► no (no leading "/")
        Step 5: send to agent
                  ├─ buildUserMessage("hello") → ChatMessage{Role: user}
                  │     Resolves image refs (`[image.png]` → bytes) and
                  │     splits inline-pasted images out of the text.
                  │
                  ├─ conv.Append(msg)
                  │     Appends to m.conv.Messages. This is the TUI's
                  │     own display copy — View() renders it as
                  │     scrollback, and PersistSession writes it to
                  │     disk. The agent does NOT read this slice on
                  │     every send; it keeps its own internal message
                  │     history. The two stay in sync via events
                  │     (see Path D). conv.Append is read-only by the
                  │     agent in one case only: ensureAgentSession
                  │     uses it to seed a freshly-started agent.
                  │
                  ├─ userInput.Reset()
                  │     Clears textarea + pending images so the user can
                  │     start the next message.
                  │
                  └─ SubmitToAgent(msg.Content, msg.Images)
                        Pushes `msg` onto the agent's INBOX (a separate
                        Go channel). The agent's own loop will read it,
                        append it to its internal history, then call the
                        LLM. That's why both calls are needed:
                          conv.Append(msg)  → makes the user SEE it
                          SubmitToAgent     → makes the agent ACT on it
                        │
                        ▼
   SubmitToAgent
        ├─ provider connected?    yes
        ├─ ensureAgentSession()    starts agent goroutine if needed
        ├─ sendToAgent ───────────► agent.Task inbox channel
        │                           (a Go channel; non-blocking push)
        │
        └─ returns ContinueOutbox cmd  (see Path D)
              That cmd, when bubbletea runs it, will read one event off
              the agent's Outbox channel and produce a tea.Msg back into
              Update. The first event normally arrives within ms once
              the LLM starts streaming.
```

## Path B — Slash command

User types `/clear`, presses **Enter**. The path overlaps with Path A
up to Step 4:

```
handleSubmit → dispatchSubmission
   Step 1..3 same as Path A
   Step 4: runSlashCommandIfMatched("/clear")
              │
              ▼
   input.NewSlashCommandController(slashCommandEnv)         slash_command.go
              │
              ▼
   SlashCommandController.HandleSubmit
              │ ParseCommand("/clear") → ("clear", "")
              ▼
   handleClearCommand(c, ctx, "")
        ├─ env.StopAgentSession()         clears agent state
        ├─ env.PersistSession()           saves current conv
        ├─ env.Conversation.Clear()       wipes display
        ├─ env.Input.Reset()
        └─ returns (result="conversation cleared", cmd=nil, nil)
              │
              ▼
   c.env.Conversation.AddNotice(result)    "conversation cleared"
   c.env.CommitMessages()                  → tea.Println to scrollback
```

A slash command's handler reads live state via `env.*` (services),
mutates UI through callbacks (e.g. `env.PersistSession`), and returns
a short `result` string the controller wraps as a notice.

Some slash commands (`/loop`, `/init`) end up calling
`env.SubmitToAgent(prompt, nil)` to hand off to the agent — they
rejoin Path A at the SubmitToAgent step.

## Path C — Background trigger

Three producers can run while no user is typing. They each park their
output in a queue/channel, then the **turn boundary** (the moment an
agent turn ends) drains them.

### Producers

```
┌─ Producer ────────────┬─ Where it lives ────┬─ Where it parks ──────────────┐
│ Cron tick             │ trigger.StartCron-  │ m.systemInput.CronQueue       │
│   (every minute the   │ Ticker (background  │   []string (prompts queued)   │
│    ticker checks      │ goroutine)          │                               │
│    durable jobs)      │                     │                               │
├───────────────────────┼─────────────────────┼───────────────────────────────┤
│ Async hook follow-up  │ trigger.StartAsync- │ m.systemInput.AsyncHookQueue  │
│   (a hook script's    │ HookTicker          │   each item carries Notice +  │
│    JSON output said   │                     │   Context lines + Contin-     │
│    `nextPrompt: ...`) │                     │   uationPrompt                │
├───────────────────────┼─────────────────────┼───────────────────────────────┤
│ Subagent completes    │ agent.SetLife-      │ m.agentEventHub → publishes a │
│   (background Task,   │ cycleHandler →      │ "task.completed" event;       │
│    spawned by the     │ hub.Publish         │ subscriber pushes it onto     │
│    Agent tool)        │                     │ m.mainEvents (a Go channel)   │
└───────────────────────┴─────────────────────┴───────────────────────────────┘
```

`m.agentEventHub` is a small pub/sub bus internal to the foreground
process — its only event today is "task.completed" from a finished
background subagent.

### Drain at turn boundary

When the live agent finishes a turn, `OnTurnEnd` calls
`drainTurnQueues` to take the next-highest-priority queued item and
play it as if the user had just typed it.

```
OnTurnEnd                                    model_agent_events.go
   └─ drainTurnQueues                        model_turn_queue.go
        First non-empty wins, priority high → low:
        │
        ├─ user input queue?  ─── parked while a turn was streaming
        │                        (handled inline; not an inject*)
        ├─ cron queue?       ──► injectCronPrompt(prompt)
        ├─ async hook queue? ──► injectAsyncHookContinuation(item)
        └─ agentEventHub batch ► injectNotification(merged hub.Message)
```

### What inject* does

Each inject* function has the same shape: tell the user what triggered
this round (a notice), make the trigger's payload look like a user
message in conv, then hand the payload to SubmitToAgent so the agent
actually responds. The "payload" varies by producer:

| Producer | Payload sent to agent |
| --- | --- |
| Cron | The cron job's `Prompt` string (what the user wrote when scheduling) |
| Async hook | The hook's `ContinuationPrompt` field |
| Subagent done | `Data` from the merged `hub.Message` — usually the subagent's final output |

```
each inject*
   ├─ conv.AddNotice(...)                          ◄── "Scheduled task fired", etc.
   ├─ conv.Append(ChatMessage{Role: user, ...})    ◄── shows in scrollback
   └─ SubmitToAgent(<payload>, nil)                ◄── kicks off the next turn
```

All three converge on **SubmitToAgent**. Same provider check, same
`ensureAgentSession`, same `sendToAgent` push. There is no other way
to reach the agent's inbox from the TUI.

## Path D — Agent → render

The agent goroutine runs the inbox, calls the LLM, streams tokens,
emits tool calls, emits a final result. Every emission goes onto its
`Outbox` channel.

```
agent goroutine                         (runs in core.Agent.Run)
   │
   ▼
core.Event onto Outbox channel
   │   Event types: OnStart, PreInfer, OnChunk (×N), PostInfer,
   │   PreTool, PostTool, OnMessage, end-of-turn, AgentStop, etc.
   │
   ▼
ContinueOutbox tea.Cmd                  agent.go: blocks on the channel,
   │                                    reads ONE event, returns a
   │                                    tea.Msg that ALSO carries the
   │                                    next ContinueOutbox cmd. So
   │                                    Update keeps scheduling fresh
   │                                    polls until events stop arriving
   │                                    (one-shot tea.Cmds simulating a
   │                                    continuous listener).
   ▼
tea.Msg (typed as a conv.* msg)
   │
   ▼
Update → routeToSubModel                update.go
   └─ conv.Update(m, &m.conv, msg)      app/conv/update.go
         │
         │ Routes by event type. The streaming flow is:
         │
         ▼
   PreInfer                             applyPreInfer
       ├─ rt.OnTurnBegin()              turn start; reset token counters
       ├─ m.Stream.Active = true
       ├─ m.Append({Role: assistant})   empty assistant message — chunks
       │                                will be appended to it
       └─ start spinner
        │
        ▼
   OnChunk (one per LLM token batch)    applyChunk
       ├─ m.AppendToLast(text)          grow the in-progress message
       └─ if chunk.Done && no tools:
              Stream.Active = false
              rt.CommitMessages()       promote to scrollback (see below)
        │
        ▼
   PostInfer                            applyPostInfer
       ├─ rt.OnTokenUsage(resp)         model_agent_events.go
       └─ if tool calls: track them
        │
        ▼
   PreTool / PostTool                   tool execution stream
       ├─ applyPreTool                  show "running tool X" spinner
       └─ applyPostTool
             └─ rt.OnToolResult(tr)     model_agent_events.go
        │
        ▼
   end-of-turn event                    rt.OnTurnEnd(result)
        │                               model_agent_events.go
        ├─ m.CommitMessages()           model_scrollback.go
        ├─ m.drainTurnQueues()          model_turn_queue.go (Path C)
        └─ fire idle hooks

   rt.OnAgentStop(err)                  turn ended (or canceled)
   rt.OnAutoCompact / OnCompactResult / OnTokenLimitResult ...
```

### Where the user sees streaming text

The terminal window has two distinct surfaces during a session:

```
   terminal native scrollback                  Bubble Tea repaint zone
   (text you can scroll up to                  (the bottom N lines
    review; never repainted —                   redrawn every Update;
    written line-by-line via                    contents discarded
    tea.Println)                                between repaints)
```

Same window, but written by two different mechanisms. Bubble Tea owns
the bottom N lines; everything above is regular terminal output it
emitted with `tea.Println`.

**While a message is streaming** the in-progress text lives in the
repaint zone, not the scrollback. Each OnChunk grows the assistant
message in `m.conv.Messages`; View() rebuilds the repaint zone every
Update so the user sees it advance token-by-token:

```
   ─── terminal scrollback (frozen) ──────────────────
     user: write a poem about the sea               ← committed
   ─── Bubble Tea repaint zone (repainted) ──────────
     assistant: Whispers of waves on ancient
                stone, the tides▮                   ← in conv.Messages,
                                                      Stream.Active=true,
                                                      NOT yet committed
     ─────────────────────────────────────────────
     ❯ (textarea waits, disabled mid-stream)
```

**When the stream finishes** (last OnChunk with `Done=true` and no tool
calls), `CommitMessages` calls `tea.Println` for the completed message.
That **lifts** the message out of the repaint zone and into the
scrollback above:

```
   ─── terminal scrollback (frozen) ──────────────────
     user: write a poem about the sea
     assistant: Whispers of waves on ancient
                stone, the tides retreat and        ← now committed,
                return, ...                            written once via
                                                       tea.Println
   ─── Bubble Tea repaint zone (repainted) ──────────
     (empty — CommittedCount caught up with len(Messages))
     ─────────────────────────────────────────────
     ❯ type your message...
```

The rule preventing the message from appearing in both places at once
is in `renderAndCommit(checkReady=true)`: it never commits the last
message while `Stream.Active == true`. So during streaming the message
is only in the repaint zone; once the stream finishes, exactly one
`tea.Println` moves it to scrollback and `CommittedCount` advances so
the repaint zone no longer redraws it.

Tool-call spinners live in the repaint zone the same way: they appear
while a tool is running and disappear once the result lands.

## File pointers

| Path step | File |
|---|---|
| `Update` dispatch | [`internal/app/update.go`](../../internal/app/update.go) |
| Keyboard handling | [`internal/app/update_keys.go`](../../internal/app/update_keys.go) |
| Submit + SubmitToAgent | [`internal/app/update_submit.go`](../../internal/app/update_submit.go) |
| Slash command controller | [`internal/app/input/slash_command.go`](../../internal/app/input/slash_command.go) |
| Slash command env builder | [`internal/app/update_command.go`](../../internal/app/update_command.go) |
| Inject paths (cron/hook/hub) | [`internal/app/model_turn_queue.go`](../../internal/app/model_turn_queue.go) |
| Agent event callbacks | [`internal/app/model_agent_events.go`](../../internal/app/model_agent_events.go) |
| Scrollback commit | [`internal/app/model_scrollback.go`](../../internal/app/model_scrollback.go) |
| Conv event router | [`internal/app/conv/update.go`](../../internal/app/conv/update.go) |
| `agent.Send` / outbox poll | [`internal/app/agent.go`](../../internal/app/agent.go) |
| Bottom UI compose | [`internal/app/view.go`](../../internal/app/view.go) |
