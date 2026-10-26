# Rendering: Model → Terminal

> 中文版本：[`rendering.zh.md`](rendering.zh.md)

Companion to [`data-flow.md`](data-flow.md). Data flow covers how input
becomes state; this doc covers how state becomes characters on screen.

> **"Render" means: return a string.** Every `Render*` function in
> this codebase returns a `string` — ANSI escape codes for color and
> style, plain UTF-8 for content. Row buffers support wrapping and retention.
> Bubble Tea's `View()` wraps the composed string and cursor in a `tea.View`;
> the framework writes it to the terminal. `tea.Println` writes above the
> inline managed frame. Fullscreen overlays use the alternate screen.

## Mental model: two surfaces

The terminal window has **two surfaces** during a session, and every
rendered string ends up on exactly one of them:

```
m.conv.Messages = [ msg0, msg1, msg2, msg3 | msg4, msg5 ]
                                            ▲
                                            CommittedCount = 4

┌─ surface ──────────────────┬─ written by ───────┬─ contents ────────────┐
│ Native terminal scrollback │ tea.Println        │ older settled rows    │
│ (frozen once written;      │ (one call per      │ — committed messages  │
│  you can scroll up to it)  │  overflow chunk)   │                       │
├────────────────────────────┼────────────────────┼───────────────────────┤
│ Bubble Tea repaint zone    │ View()             │ recent settled rows + │
│ (fills terminal height;    │ (called every      │ msg4..msg5 + spinners │
│  on every Update)          │  Update)           │ input strip           │
└────────────────────────────┴────────────────────┴───────────────────────┘
```

A **streaming** assistant reply lives in the repaint zone while
`Stream.Active == true`. `CommitMessages` moves its settled rendering into
the bounded recent-row buffer and advances `CommittedCount`, so the active
message renderer stops drawing it. Only rows overflowing that buffer enter
the serialized `tea.Println` queue. Short replies therefore remain visible
above the fixed composer after completion. The rule preventing premature commits is in
`renderAndCommit(checkReady=true)`: never commit the message owned by `Stream.MessageID` while
`Stream.Active` is true. Notices after it remain visible in the live view.

The buffer stores lines before terminal soft wrapping. Each `Update` moves
settled rows that no longer fit beside live output, activity, suggestions,
or a docked modal into the print queue. Resizing wraps the original retained
lines again, so widening removes the breaks introduced by narrowing.
Activity indicators use only the rows left after reserving the composer or
docked modal, with the same cap in the view and overflow calculation.
`/clear` drops retained rows and cancels queued prints. If a `Println` has
already been issued, its completion triggers the terminal wipe before any
new print; monotonic print IDs keep stale messages from reviving cleared output.

**Both surfaces share the same render functions.** `RenderMessageAt`
is what produces each message's string; what differs is the index
range (settled rendering: `0..CommittedCount`; active rendering:
`CommittedCount..len(Messages)`). The repaint zone also includes retained
settled rows, without rendering those messages again.

## Production rendering and interaction contracts

Agent calls keep one round-prefix identity from launch through completion.
Spawn previews and worker prompts use the full configured name, with `Agent` as
the default; the runtime's shortened name does not replace that UI identity.
Compact running rows show real current activity and elapsed execution time;
unstarted and approval-blocked calls stay static. The scroll fallback shares the
conversation renderer so it cannot revive stale activity or a `Pending` state.
Height-bounded worker views shorten the identity to one row before allocating
space to current activity or approval state. Agent approval previews wrap within
their inset, preserving continuation indentation and inspectable prompt prose.
Model, mode, usage, and routine
process metadata belong to the explicit detail view, not the default task flow.
Foreground completion replaces activity with the existing result text (bounded
when long), without an extra `Completed` / `Finished` row. Background launch is
only a handoff acknowledgment, attached with the result's `└` connector rather
than a success checkmark; its outcome arrives as one concise existing
notification, not another Agent block or a second stop receipt. Terminal task
details drop stale progress and routine cancellation errors, retaining meaningful
findings and genuine reasons. Explicit expansion retains original content and
usage details; telemetry and persisted results are unchanged.
Errors, exit codes, and test results remain actionable output, not metadata to
suppress. Keep the upstream `Memory ⟡` and `Recap ※` identities and meaningful
recap facts; do not add repeated transcript-access hints.

Asynchronous notices remain conversation content, ahead of a standalone waiting
indicator at the live tail. This is presentation-only: actual streamed text,
reasoning, message ownership, and persisted transcript order are not rearranged.
The inference indicator stays alive through text and tool-input streaming until
inference ends. Normally it is only a breathing `✦ 17s`: animation means UI
liveness, not model progress. After 30 seconds without incoming text, reasoning,
or changed tool-input progress, it adds `Waiting for model`; real incoming data
clears that explanation. UI ticks, notices, and duplicate progress do not reset
the silence clock. Real retries may show `Retrying · attempt N`. An approval or
question modal owns its explanation, with no duplicate inference waiting row.
These states neither invent model progress nor change timeout/retry policy, and
are never committed to terminal history.

Edit/Write results retain their full numbered inline diff, semantic backgrounds,
and two-column inset aligned with the `  └` footer. New files report
`└ new file · +29 lines` (singular `line` for one; `0 lines` for an empty file).
Existing-file changes show green additions and red removals, omitting zero
counts; a zero/zero result says `no changes`. Both nested and standalone result
renderers use the same summary formatter.

The tasks component is display-only: the root app owns the task manager and
routes actions. `/tasks` supports inspection, details, and stopping an exact task
ID and execution attempt only after confirmation, with **Back** selected by default.
`r` review reveals the current mixed FIFO of approvals, questions, and other
interactions; it does not reorder requests. Pending plan items are not queued
runtime work. Approval state is associated with the actual originating task ID,
not inferred from the currently selected row. `c` asks main to choose a safe
continuation for an eligible completed or failed Agent task in its existing child
session. It does not replay commands directly, bypass approval, or continue a
deliberately stopped/killed task.

Base approvals offer **Allow once**, **Deny**, and, only when eligible, **More
permissions**. Broader grants require a separate confirmation defaulting to
**Back**. Tab and Shift-Tab navigate; they never grant immediately. Esc backs out
of nested choices and denies at the base prompt. Worker approvals remain
`OnceOnly`; persistent grants are exact `AllowRules` written to
`.san/settings.local.json`, not broad implicit trust.

Entrypoints are the root app interaction router and `/tasks` command, with
`internal/app/input` owning prompts and `internal/app/conv` owning result
rendering. Regression coverage lives in the corresponding input, task-viewer,
interaction, and conversation-renderer tests. Root integration must preserve
these contracts when composing the display-only components.

## View() composes the repaint zone

`(*model).View()` in [`internal/app/view.go`](../../internal/app/view.go)
runs after every `Update` and returns the string for the repaint zone.

```go
func (m *model) View() tea.View {
    //   ^ Go method on *model; `m` is the instance (Go's
    //     equivalent of `this`/`self`). The whole codebase uses `m`
    //     for the foreground model.
    ...
}
```

**No input parameters** — that's a Bubble Tea contract. Everything
View reads comes from `m`'s fields. The sub-renderers it calls
(`RenderActiveContent` etc.) take a `conv.RenderContext` struct, which
`m.messageRenderParams()` assembles from `m`'s fields on each call.

View() picks one of four layouts, top-down:

```
View()
  1. !m.env.Ready              ──► "\n  Loading..."
  2. active popup?             ──► popup.Render() — fullscreen
                                   (slash-command pickers: /models,
                                   /tools, /skills, ...)
  3. active modal?             ──► modal.Render() wrapped between
                                   separator bars
                                   (Question modal, Approval modal)
  4. otherwise (normal mode) ──► renderNormalView()
        ├─ chat section        ── recent settled rows + conv.RenderActiveContent
        ├─ spare space         ── keeps short conversations stable at the top
        ├─ activity indicators ── tracker, compact and self-learning status
        ├─ queue preview       ── if input was queued during a stream
        ├─ suggestion list     ── /-command and @-file autocomplete
        ├─ separator
        ├─ textarea            ── grows upward as input wraps
        ├─ separator
        └─ status line         ── model name, tokens, mode
```

Popups (full-screen) and modals (wrapped) look like the same idea but
have different render flows because the chat content stays visible
behind a modal, not behind a popup.

## How a single message is rendered

`RenderMessageAt(ctx, idx, isStreaming)` dispatches by `msg.Role`:

```
                ┌── Role: User ──┐
                │                │
                │ ToolResult?    ──► RenderToolResultInline
                │ otherwise      ──► RenderUserMessage
                │                     (text + images, md-rendered)
                │
RenderMessageAt ─┤
                │
                ├── Role: Notice ──► RenderSystemMessage
                │                     (plain text, muted color)
                │
                └── Role: Assistant ──► renderAssistantWithTools
                                          ├─ assistant text + thinking
                                          │   (md-rendered)
                                          └─ tool-calls block (each
                                              call + its inlined
                                              result, if available)
```

Reasoning is gated by the `thinkingDisplay` preference
([configuration.md](../reference/configuration.md)), and the gate sits on **both**
surfaces: `RenderAssistantMessage` for the live tail and `FlushStreamingBlocks`
for the scrollback commit. `full` (the default) draws the body; `collapsed` draws
the body nowhere and commits one `✦ Thought for 3.2 s` line instead; `hidden`
draws nothing. In the suppressed modes the flush still advances
`ThinkingCommittedLen` / `ThinkingEmitted` past the bytes it did not print, so a
later full rebuild cannot resurrect the body it never showed.

A response can alternate between commentary and reasoning. `Stream.ThinkingActive`
tracks the latest text/thinking delta: a new text delta finishes the current
reasoning paragraph, while a new thinking delta keeps its unfinished paragraph
live even if earlier commentary remains in the message. Testing only whether
`Content` is nonempty would commit each resumed reasoning token as its own
scrollback block, displaying one word per line with blank lines between them.

`renderAssistantWithTools` does **not** scan the message list to find
its paired results — `ctx.InlinedResults` was precomputed once at the
top of the render pass and tells it which `ToolCallID → ToolResultData`
entries to inline. See "Tool calls and inlined results" below.

## Markdown via MDRenderer

[`internal/app/conv/markdown.go`](../../internal/app/conv/markdown.go)
wraps [glamour](https://github.com/charmbracelet/glamour). Five
behaviors are intentional and not glamour defaults:

| Concern | Behavior |
| --- | --- |
| Width | Built for the current terminal width, minus 4 for the `● ` indent. `ResizeMDRenderer` rebuilds it on `WindowSizeMsg`. |
| Background | Auto-detects dark vs light. `rebuildIfNeeded()` rebuilds inside `Render` if the terminal flipped themes. |
| Tables | Pulled out before glamour sees them; rendered with lipgloss table primitives for full border control. |
| Soft line breaks | LLMs hard-wrap at ~80 cols. Soft-wrapped paragraphs get joined before glamour so it can re-wrap at the real width. |
| Inline tokens | A custom inline-markdown pass styles things glamour handles poorly (e.g. backticks inside other formatting). |

Width matters: glamour computes column widths from its configured
width. If the terminal resizes, glamour-wrapped content already in
scrollback is now sized for the old width while the repaint zone uses
the new width. That is left alone: the terminal rewraps what it already
holds, and committed rows cannot be taken back (see Resize below).

## Tool calls and inlined results

[`internal/app/conv/tool_render.go`](../../internal/app/conv/tool_render.go)
renders the tool-calls block under an assistant message:

```
● Read(src/foo.go)                      ← tool name + summary args
  └ 128 lines                          ← collapsed result summary
● Bash (run the tests)
    $ cd web                           ← "$" only on the first command row
      npm test
  │ ✓ src/foo.test.ts (12)              ← the output's last bashPreviewLines
  │ ✓ src/bar.test.ts (8)
  └ last 2 of 3 lines
```

Command input uses a continuous rectangular `CommandBg` surface, shared by the
transcript and approval preview. It has a two-column unpainted outer inset and
two columns of inner padding; every visual row fills the same width, including
explicit blank lines. Output keeps the terminal canvas, with no extra blank row
between command and output. Continuations preserve the original indentation.
Edit and Write surfaces use the same two-column outer inset as the result's `└`
connector, without a leftover rail gutter. Numbered `-`/`+` rows keep red/green
backgrounds, including new-file Write previews and blank lines. Long diff lines
wrap without losing content or inventing new line numbers. The app does not
paint the whole terminal background to simulate a theme.

State that drives it:

- **Pending vs done** — a tool call sits in `m.conv.Tool.PendingCalls`
  until its `ToolResult` arrives. While pending, the tool name shows
  a spinner.
- **Expanded / collapsed** — per-message `Expanded` flag, toggled by
  Ctrl-O. Nested Read results collapse to a summary; shell results keep
  their last `bashPreviewLines` of output, the same rows the running call
  showed from its progress, so the row does not jump when it finishes.
  Expanded content shares the summary's connector column.
- **Error** — nested failures show their diagnostic and a tinted `failed` trailer.
- **Parallel mode** — when multiple tool calls run in parallel, each
  call shows its own progress. Completion is tracked by call ID, since results
  arrive in completion order. The batch stays live until every call finishes,
  then calls and their paired results commit to scrollback together. Cancellation
  closes only unfinished calls, regardless of their position in the batch.

The pairing between an assistant's tool calls and their result messages
is precomputed by `PrecomputeInlinedResults(messages, from)` and lives on
`RenderContext.InlinedResults`. Three lookups consume it:

```
InlinedResults.ownerOf(resultIdx)      // which assistant owns this result?
                                        // used by RenderMessageRange to skip
                                        // the result (it's drawn inline)

InlinedResults.resultsFor(assistantIdx) // (callID → ToolResultData) for an
                                        // assistant; used by
                                        // renderAssistantWithTools

InlinedResults.IsResultInlined(idx)     // is this result already going to
                                        // be drawn under its owner?
                                        // used by RenderSingleMessage to
                                        // skip standalone Println
```

One pass over the message list, three consumers, zero re-scanning.

## Worked example: streaming reply + tool call

End-to-end trace. The user typed `list files` and pressed Enter; that
part is the input flow ([data-flow.md](data-flow.md) Path A). Below
picks up at the moment the agent goroutine starts emitting events.

`conv.Messages` starts as `[user "list files"]` with
`CommittedCount=1` (the user message was already committed by the
Enter handler).

### Step 1 — PreInfer: open an empty assistant stub

```
event:           sdkagent.InferenceStart
applyPreInfer:   m.Stream.Stop(); rt.CommitMessages()
                 msg := m.Append({Role: assistant, Content: ""})
                 m.Stream = {Active: true, MessageID: msg.ID}
                 start spinner

conv.Messages:   [user, assistant{Content:""}]
CommittedCount:  1   (only user is committed so far)
```

View() runs after this Update:

```
View → renderNormalView
     → conv.RenderActiveContent(ctx)
       ctx.InlinedResults = PrecomputeInlinedResults(Messages)
         = {} (no ToolCalls anywhere yet)
       → RenderMessageRange(ctx, startIdx=1, endIdx=2, includeSpinner=true)
         i=1: ownerOf(1) = -1 (not a result) → don't skip
              isStreaming = (msg.ID == Stream.MessageID && Stream.Active)
                          = true
              → RenderMessageAt(ctx, 1, isStreaming=true)
                → renderAssistantWithTools(ctx, msg, 1, isLast=true)
                  → RenderAssistantMessage(content="", streamActive=true,...)
                    returns the "● ▮" stub
                  msg.ToolCalls == nil → just return base
         + pending-tool spinner
```

Repaint zone shows `● ▮ ⋯`. Scrollback unchanged.

### Step 2 — OnChunk (text): grow the message

```
event:           sdkagent.InferenceUpdate{Delta: {Block: {Type: text, Text: "I'll list them with ls."}}}
applyChunk:      m.AppendStream(text, "")

conv.Messages:   [user, assistant{Content:"I'll list them with ls."}]
Stream.Active:   still true (Done=false)
```

Same call chain as Step 1, but `RenderAssistantMessage` now has
non-empty content and `MDRenderer.Render` styles it. Repaint zone:
`● I'll list them with ls. ▮ ⋯`. More OnChunks may follow — each is
`AppendStream` + a View() repaint.

### Step 3 — PostInfer: tool calls land on the assistant message

```
event:           sdkagent.InferenceEnd{Response: {Content: [ToolCall{ID:"tc-1", Name:"Bash", Input:{cmd:"ls"}}]}}
applyPostInfer:  rt.OnInference(resp)
                 m.SetStreamToolCalls(resp.ToolCalls)
                 m.Tool.Track(resp.ToolCalls)

conv.Messages:   [user,
                  assistant{Content:"I'll list them with ls.", ToolCalls:[tc-1]}]
```

`renderAssistantWithTools` now takes the second branch:

```
base = RenderAssistantMessage(...)             ← the text part
msg.ToolCalls != nil
resultMap = ctx.InlinedResults.resultsFor(1)
          = nil                                 ← tc-1 hasn't finished
RenderToolCalls(ToolCallsParams{
  ToolCalls:    [tc-1],
  ResultMap:    {},                             ← nil → empty
  PendingCalls: [tc-1],                         ← spinner driver
  CurrentIdx:   0,
  SpinnerView:  "⋯",
  ...
})
```

Repaint zone now shows:

```
● I'll list them with ls.
  ⋯ Bash(ls)
```

### Step 4 — PostTool: result arrives, gets inlined

```
event:           sdkagent.ToolEnd{ID:"tc-1", Result: {Content:"file1\nfile2"}}
m.ProcessToolResult(tr):
  applyToolSideEffects(...)
  firePostToolHook(...)
  (the agent appends the ToolResult as a user-role message)

conv.Messages:   [user "list files",
                  assistant{Content+ToolCalls:[tc-1]},
                  user{ToolResult:{ToolCallID:"tc-1", Content:"file1\nfile2"}}]
```

View() rebuilds `ctx`. **InlinedResults earns its keep:**

```
PrecomputeInlinedResults(Messages):
  i=1 is an assistant with ToolCalls [tc-1]; scan forward:
    j=2: ToolResult.ToolCallID == "tc-1" → pair
  resultOwner         = {2: 1}
  resultsForAssistant = {1: {"tc-1": ToolResultData{Content:"file1\nfile2", ...}}}

RenderMessageRange(ctx, 1, 3, includeSpinner=true):
  i=1 (assistant):
    ownerOf(1) = -1 (not a result) → render
    renderAssistantWithTools:
      resultMap = resultsFor(1) = {"tc-1": ToolResultData{...}}    ← populated now
      RenderToolCalls draws "● Bash(ls)" with the file listing INLINE below
  i=2 (ToolResult):
    ownerOf(2) = 1, which is >= startIdx → SKIP
    (already drawn under its owning assistant; standalone render would duplicate)
```

Repaint zone:

```
● I'll list them with ls.
  ● Bash(ls)
      ⎿  file1
         file2
```

### Step 5 — OnChunk(Done): promote the block to scrollback

```
event:           sdkagent.InferenceEnd{Response: {...}}
applyChunk:      m.AppendStream(...)       (possibly a final text chunk)
                 if chunk.Done && no tool calls remaining:
                     m.Stream.Active = false
                     return rt.CommitMessages()
```

`CommitMessages → renderAndCommit(checkReady=true)`:

```
for i in CommittedCount..len(Messages):    // i = 1, 2
  msg = Messages[i]
  if checkReady && i == StreamMessageIndex() && Stream.Active:
      break                                  // but Stream.Active is now false
  rendered = conv.RenderSingleMessage(ctx, i)
    i=1: RenderMessageAt(ctx, 1, false)      // no longer streaming → no cursor
         returns the same assistant + tool block as before
    i=2: msg.ToolResult != nil
         InlinedResults.IsResultInlined(2) = true → return ""       ← skipped
  if rendered != "": append to parts

m.queueScrollbackPrint(strings.Join(parts, "\n"), preCommitRows)
                                             // retain recent rows, print overflow
CommittedCount = 3                           // caught up
```

What changed on screen:

- **Recent settled rows** gain the assistant and tool block.
- **Active messages** are now empty (`CommittedCount == len(Messages)`).
- The next `View()` shows the retained block above the bottom-fixed input.
  Older overflowing rows enter native scrollback in safe chunks.

The user watched the same string grow in the repaint zone; its settled
rendering remains visible and eventually enters native history once. The
`IsResultInlined` short-circuit in `RenderSingleMessage` is what stops
the ToolResult from also being Println'd standalone.

## Resize behavior

**Already-committed scrollback is immutable to us.** `handleWindowResize` in
[`internal/app/update_resize.go`](../../internal/app/update_resize.go) updates
`m.env.Width / Height`, drops the frozen print frame (so `insertAbove` cannot
scroll live rows into history under the old geometry), rebuilds the markdown
renderer at the new width, and lets Bubble Tea repaint the bottom strip. It does
not reprint committed messages: the terminal rewraps what it already holds on its
own, and a row that reached the top of the screen cannot be taken back — see
[ADR-0002](../design/decisions/0002-native-scrollback-commit-protocol.md), whose
invariant 6 covers the rewrap arithmetic the redraw depends on.

Recent settled rows have not entered native history yet. Resize rewraps these
rendered rows and trims their buffer to the new space above the composer,
queuing only the oldest overflow. During a history insert, the fixed-height
normal frame yields one row while retaining the live tail and composer. The
one-row print chunk is selected before capturing this frozen frame, so the
handoff copy matches the inserted row. This prevents the conversation flashing
blank while the existing FIFO protocol keeps live UI out of native history.
The terminal writer buffers the temporary redraw and history insert into one
write; synchronized output, when enabled, stays open across both. Queued rows
are parsed once and consumed by chunk, with remaining rows rewrapped only after
a width change.

`handleWindowResize` is also the **deferred initial paint**: the first
`WindowSizeMsg` is where a resumed conversation is committed, via
`commitAllMessages()`, all of it.

## Inspecting completed tool results

Native scrollback stays immutable. Idle Ctrl+O and `/history` instead open the
existing transcript overlay in the alternate screen. `model_history.go` reads
`session.Store.LoadHistory` asynchronously; the transcript projector follows the
same selected parent chain as resume but continues past compaction boundaries.
The normal conversion and tool-result blob hydration also apply. Before a
transcript has been saved, the current conversation supplies the view. Other
read failures are displayed instead of silently hiding older history.

The app supplies one rendered entry per message or tool call to
`input.TranscriptViewer`, using `RenderSingleMessage`, `RenderToolCalls` and
`RenderToolResultInline`. Streaming commit offsets are cleared on the display
copy. Parallel results pair by call ID and expand independently. The viewer
starts at the latest tool, expanded; Left/Right selects a tool, Enter toggles
its result, and the usual scrolling keys navigate the full conversation.
Changing terminal width re-renders entries at the new width. Scroll and tool
selection reuse rendered lines; expansion re-renders only the selected entry.
The viewer wraps any remaining overlong lines before caching rows and offsets,
so every part of the output is reachable by vertical scrolling. Edit and Write
diffs also follow the viewer's expansion state; native scrollback still prints
their complete stored diffs.

The viewer owns only temporary selection, expansion and rendered lines. Closing
releases them and restores the original prompt and cursor. A generation token
discards asynchronous loads after close/reopen. Neither loading nor navigation
changes `CommittedCount`, native print queues, the model's context or execution
state. New messages appear the next time the viewer opens. No setting or new
dependency is required.

Regression coverage lives in `model_history_test.go`,
`input/transcript_view_test.go`, `session/history_test.go` and the transcript
projector tests: completed and resumed results, independent parallel expansion,
compaction, persisted full output, resize, stale loads and input preservation.

## File pointers

| Concern | File |
| --- | --- |
| `View()` composition | [`internal/app/view.go`](../../internal/app/view.go) |
| Per-message rendering + pairing | [`internal/app/conv/view.go`](../../internal/app/conv/view.go) |
| User / assistant / notice rendering | [`internal/app/conv/message.go`](../../internal/app/conv/message.go) |
| Markdown rendering | [`internal/app/conv/markdown.go`](../../internal/app/conv/markdown.go) |
| Tool call / result rendering | [`internal/app/conv/tool_render.go`](../../internal/app/conv/tool_render.go) |
| Compact / agent activity / tracker | [`internal/app/conv/compact.go`](../../internal/app/conv/compact.go), [`agent_to_ui.go`](../../internal/app/conv/agent_to_ui.go), [`tracker_view.go`](../../internal/app/conv/tracker_view.go) |
| `MDRenderer` lifecycle | [`internal/app/conv/model.go`](../../internal/app/conv/model.go) |
| Scrollback commit | [`internal/app/model_scrollback.go`](../../internal/app/model_scrollback.go) |
| History inspection | [`internal/app/model_history.go`](../../internal/app/model_history.go), [`internal/app/input/transcript_view.go`](../../internal/app/input/transcript_view.go) |
| Resize | [`internal/app/update_resize.go`](../../internal/app/update_resize.go) |
