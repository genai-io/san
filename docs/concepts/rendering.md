# Rendering: Model → Terminal

> 中文版本：[`rendering.zh.md`](rendering.zh.md)

Companion to [`data-flow.md`](data-flow.md). Data flow covers how input
becomes state; this doc covers how state becomes characters on screen.

> **"Render" means: return a string.** Every `Render*` function in
> this codebase returns a `string` — ANSI escape codes for color and
> style, plain UTF-8 for content. No off-screen buffers, no canvases.
> Bubble Tea's `View()` returns a string and the framework writes it
> to the terminal; `tea.Println` takes a string and writes it above
> the alt-screen region. The "rendering pipeline" below is entirely
> string composition; the terminal does the actual drawing.

## Two render paths, same render functions

Every visible byte in the TUI is produced by code in
`internal/app/conv/view.go` — `RenderMessageAt`, `RenderUserMessage`,
`RenderAssistantMessage`, `RenderToolCalls`, and friends. But the
**output is consumed by two completely different mechanisms**:

```
┌─ Rendering primitives in internal/app/conv ───────────────┐
│                                                            │
│   RenderMessageAt  ─┬─  Markdown via MDRenderer            │
│   RenderUserMsg     ├─  Tool call + result blocks           │
│   RenderToolCalls   ├─  System notices                      │
│   ...               └─  Spinners / progress indicators     │
│                                                            │
└────────────────┬──────────────────┬───────────────────────┘
                 │                  │
                 ▼                  ▼
   ┌────────────────────┐   ┌──────────────────────┐
   │  CommitMessages    │   │  View()              │
   │  → tea.Println     │   │  → bubbletea repaint │
   │                    │   │                      │
   │  committed         │   │  active content      │
   │  messages only     │   │  (uncommitted +      │
   │  (Stream.Active    │   │   spinners + modals  │
   │  == false)         │   │   + input strip)     │
   └────────────────────┘   └──────────────────────┘
                 │                  │
                 ▼                  ▼
   ┌────────────────────┐   ┌──────────────────────┐
   │ Native terminal    │   │ Bottom N lines       │
   │ scrollback (frozen │   │ (redrawn every       │
   │ once written)      │   │  Update)             │
   └────────────────────┘   └──────────────────────┘
```

The split lets streaming text and tool spinners update live (repaint
zone) while finished messages stay frozen in scrollback the user can
scroll up to.

## What View() composes

`(*model).View()` in `internal/app/view.go` is called after every
`Update`. It picks one of three top-level layouts:

```
View() decides which layout, top-down:

  1. Is a popup active?        ──► render that popup fullscreen
     (provider picker, tools picker, etc. — see data-flow Path A)

  2. Is a modal active?        ──► render modal between separator bars
     (Question modal, Approval modal)

  3. Otherwise (normal mode):
        chat section          ◄── RenderActiveContent
        turn usage line
        separator
        queue preview         ◄── if user queued input while streaming
        textarea              ◄── m.userInput.RenderTextarea()
        suggestion list       ◄── autocomplete entries shown below
                                  the textarea when you type "/" or
                                  "@<filename>" etc. (m.userInput.Suggestions)
        separator
        status line           ◄── model name, tokens, mode
```

Popups and modals are the same idea (UI that pops up over the input)
but use different render flows because modals stack with the chat
above and below them, while popups take the whole screen.

## How a single message is rendered

`RenderMessageAt(params, idx, isStreaming)` dispatches by
`msg.Role`:

```
              ┌─ Role: User ─┐
              │              │
              │  has ToolResult?   ──► RenderToolResultInline
              │  else              ──► RenderUserMessage
              │                          (text + images, md-rendered)
              │
RenderMessageAt
              │
              ├─ Role: Notice ──► RenderSystemMessage
              │                   (plain text, muted color)
              │
              └─ Role: Assistant ──► renderAssistantWithTools
                                       ├─ content + thinking (md)
                                       ├─ tool calls block
                                       └─ paired tool results
```

`renderAssistantWithTools` walks forward from this message's index to
collect every immediately-following ToolResult message — those are
the results paired to this assistant turn's tool calls — and renders
them as a single tool-calls block beneath the assistant text.

## Markdown rendering (MDRenderer)

`internal/app/conv/markdown.go` wraps [glamour](https://github.com/charmbracelet/glamour)
with two non-default behaviors:

| Concern | What MDRenderer does |
| --- | --- |
| Width | Built per terminal width (`width − 4` to account for the `● ` indent). `ResizeMDRenderer` rebuilds it on `WindowSizeMsg`. |
| Background | Auto-detects dark vs light terminal background. `rebuildIfNeeded()` rebuilds on every `Render` if it changed. |
| Tables | Extracted before glamour sees them; rendered with lipgloss table primitives for full border control. |
| Soft line breaks | LLMs hard-wrap at ~80 cols; the renderer joins soft-wrapped paragraphs so glamour can re-wrap at actual width. |
| Inline tokens | Custom inline-markdown pass for parts glamour doesn't style well (e.g., backticks inside other formatting). |

Why width matters for rendering: glamour computes column widths from
its configured width and wraps text. If width changes (terminal
resize), wrapped content from the old width still sits in scrollback
— but the bottom repaint zone uses the new width. That's the source
of the "scrollback looks weird after resize" issue and why
`handleWindowResize` calls `reflowScrollback`, which **rerenders
every committed message at the new width and clears + redraws
scrollback**.

## Tool calls and results

`internal/app/conv/tool_render.go` renders the assistant's tool calls:

```
● Bash(npm test)                        ← tool name + summary args
    ⎿  > vitest run                     ← collapsed result preview
        ✓ src/foo.test.ts (12)
        ✓ src/bar.test.ts (8)
       … 47 more lines (Ctrl-O to expand)
```

State that drives this:

- **Pending vs done**: each tool call is in `m.conv.Tool.PendingCalls`
  until its `ToolResult` arrives. While pending, a spinner shows next
  to the tool name.
- **Expanded / collapsed**: per-message `Expanded` flag toggled by
  Ctrl-O. Collapsed shows a preview + line count; expanded shows full
  content.
- **Error**: `ToolResult.IsError` flips the icon (✓ → ✗) and tints
  the result.
- **Parallel mode**: when multiple tool calls run in parallel, the
  block changes layout (each call shows its progress separately).

## Active content vs scrollback — same renderer, two consumers

The active-content zone (mid-screen, repainted every Update) and the
scrollback (above, written via `tea.Println`) both use
`RenderMessageAt`. The difference is **which range of messages they
render**:

```
m.conv.Messages = [ msg0, msg1, msg2, msg3, msg4, msg5 ]
                                          ▲
                                          CommittedCount=4

scrollback (already written):     msg0, msg1, msg2, msg3
active content (View repaints):   msg4, msg5    +  spinner if any
                                  ──────────
                                  if msg5 is a streaming assistant
                                  msg, RenderMessageRange passes
                                  isStreaming=true so the renderer
                                  shows a cursor and skips the
                                  "done" suffix.
```

`CommitMessages` advances `CommittedCount` and emits one
`tea.Println` for each newly-committed message — so the very same
rendered string moves from "active content" into "scrollback" once
the message finishes streaming. The user sees a single visual
transition, not a duplicate.

## Worked example: streaming reply + tool call

Concrete trace of one path through the rendering pipeline. The user
typed `list files` and pressed Enter; that part is the input flow
([data-flow.md](data-flow.md) Path A). Below picks up at the moment
the agent goroutine starts emitting events.

`conv.Messages` starts as `[user "list files"]` with
`CommittedCount=1` (the user message was already committed to
scrollback by the prior Enter handler).

### Step 1 — PreInfer: open an empty assistant stub

```
event:           core.PreInfer
applyPreInfer:   rt.OnTurnBegin()
                 m.Stream.Active = true
                 m.Append({Role: assistant, Content: ""})
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
       → RenderMessageRange(ctx, 1, 2, includeSpinner=true)
         i=1: ownerOf(1) = -1 (not a result) → don't skip
              isStreaming = (1 == lastIdx && Stream.Active && role==assistant)
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
event:           core.OnChunk{Text: "I'll list them with ls.", Done: false}
applyChunk:      m.AppendToLast(text, "")

conv.Messages:   [user, assistant{Content:"I'll list them with ls."}]
Stream.Active:   still true (Done=false)
```

View() repaints. The same call chain as Step 1, but now
`RenderAssistantMessage` sees non-empty content; `MDRenderer.Render`
styles it. Repaint zone shows `● I'll list them with ls. ▮ ⋯`.

A handful more OnChunks may arrive; each is an `AppendToLast` plus a
View() repaint.

### Step 3 — PostInfer: tool calls land on the assistant message

```
event:           core.PostInfer{Response: {ToolCalls: [{ID:"tc-1", Name:"Bash", Input:{cmd:"ls"}}]}}
applyPostInfer:  rt.OnTokenUsage(resp)
                 m.SetLastToolCalls(resp.ToolCalls)
                 m.Tool.Track(resp.ToolCalls)

conv.Messages:   [user,
                  assistant{Content:"I'll list them...", ToolCalls:[tc-1]}]
```

View() repaints. Now `renderAssistantWithTools` takes its second
branch:

```
renderAssistantWithTools(ctx, msg, 1, isLast=true)
  base = RenderAssistantMessage(...)                 ← the text part
  msg.ToolCalls != nil
  resultMap = ctx.InlinedResults.resultsFor(1)
            = nil                                     ← tc-1 hasn't finished yet
  RenderToolCalls(ToolCallsParams{
    ToolCalls: [tc-1],
    ResultMap: {},                                    ← nil → empty map
    PendingCalls: [tc-1],                             ← spinner driver
    CurrentIdx: 0,
    SpinnerView: "⋯",
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
event:           core.PostTool{Result: {ToolCallID:"tc-1", Content:"file1\nfile2"}}
m.ProcessToolResult(tr):
  applyToolSideEffects(...)
  firePostToolHook(...)
  (the agent appends the ToolResult as a user-role message)

conv.Messages:   [user "list files",
                  assistant{Content+ToolCalls:[tc-1]},
                  user{ToolResult: {ToolCallID:"tc-1", Content:"file1\nfile2"}}]
```

View() rebuilds the render context. **This is where InlinedResults
earns its keep:**

```
PrecomputeInlinedResults(Messages):
  i=1 is an assistant with ToolCalls [tc-1]
  scan forward:
    j=2: ToolResult with ToolCallID=tc-1, owned → pair
  resultOwner          = {2: 1}
  resultsForAssistant  = {1: {"tc-1": ToolResultData{Content:"file1\nfile2", ...}}}

RenderMessageRange(ctx, 1, 3, includeSpinner=true):
  i=1 (assistant):
    ownerOf(1) = -1 (not a result) → render
    renderAssistantWithTools:
      resultMap = resultsFor(1) = {"tc-1": ToolResultData{...}}   ← now populated
      RenderToolCalls draws "● Bash(ls)" with the file listing INLINE below
  i=2 (ToolResult):
    ownerOf(2) = 1, which is >= startIdx → SKIP
    (the result is already drawn under its owning assistant; rendering
     it standalone would duplicate)
```

Repaint zone now shows:

```
● I'll list them with ls.
  ● Bash(ls)
      ⎿  file1
         file2
```

### Step 5 — Final OnChunk + commit to scrollback

```
event:           core.OnChunk{Done: true, Response: {...}}
applyChunk:      m.AppendToLast(...)       (possibly a final text chunk)
                 if chunk.Done && no tool calls remaining:
                     m.Stream.Active = false
                     return rt.CommitMessages()
```

`CommitMessages → renderAndCommit(checkReady=true)`:

```
for i in CommittedCount..len(Messages):    // i = 1, 2
  msg = Messages[i]
  if checkReady && i == lastIdx && role==assistant && Stream.Active:
      break                                  // but Stream.Active is now false
  rendered = conv.RenderSingleMessage(ctx, i)
    i=1: RenderMessageAt(ctx, 1, false)      // not streaming → no cursor
         returns the same assistant+tool block as before
    i=2: msg.ToolResult != nil
         InlinedResults.IsResultInlined(2) = true → return ""    ← skipped
  if rendered != "": append to parts

tea.Println(strings.Join(parts, "\n"))       // ONE Println, one block
CommittedCount = 3                           // caught up
```

Effect on the screen:

- **Native scrollback** gains one new block: `● I'll list them with ls. / ● Bash(ls) / ⎿ file1 / file2`. Frozen there until terminal cleared.
- **Repaint zone** is now empty (CommittedCount equals len(Messages)).
- Next View() call paints just the input strip — ready for the next user prompt.

The same rendered string that the user was watching grow in the
repaint zone is now living in scrollback, written exactly once via
`tea.Println`. The `IsResultInlined` short-circuit in
`RenderSingleMessage` is what stops the ToolResult from also being
Println'd standalone.

## Resize behavior

Terminal resize is the only event that invalidates already-painted
scrollback (because glamour wrapping is width-specific).
`handleWindowResize` in `internal/app/update_resize.go`:

1. Updates `m.env.Width / Height` and the textarea width.
2. Calls `m.conv.ResizeMDRenderer(newWidth)` to rebuild glamour.
3. If width actually changed and any messages are committed:
   `reflowScrollback` clears the screen and re-Printlns every
   committed message at the new width.
4. Returns the cmd; bubbletea calls View() to repaint the new bottom
   strip at the new width.

## File pointers

| Concern | File |
| --- | --- |
| `View()` composition | [`internal/app/view.go`](../../internal/app/view.go) |
| Per-message rendering | [`internal/app/conv/view.go`](../../internal/app/conv/view.go) |
| User / assistant / notice rendering | [`internal/app/conv/message.go`](../../internal/app/conv/message.go) |
| Markdown rendering | [`internal/app/conv/markdown.go`](../../internal/app/conv/markdown.go) |
| Tool call / result rendering | [`internal/app/conv/tool_render.go`](../../internal/app/conv/tool_render.go) |
| Compact / progress / tracker rendering | [`internal/app/conv/compact.go`](../../internal/app/conv/compact.go), [`progress.go`](../../internal/app/conv/progress.go), [`tracker_view.go`](../../internal/app/conv/tracker_view.go) |
| `MDRenderer` lifecycle | [`internal/app/conv/model.go`](../../internal/app/conv/model.go) |
| Scrollback commit | [`internal/app/model_scrollback.go`](../../internal/app/model_scrollback.go) |
| Resize + reflow | [`internal/app/update_resize.go`](../../internal/app/update_resize.go) |
