# Rendering: Model → Terminal

> 中文版本：[`rendering.zh.md`](rendering.zh.md)

Companion to [`data-flow.md`](data-flow.md). Data flow covers how input
becomes state; this doc covers how state becomes pixels on screen.

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
        suggestions overlay   ◄── if autocomplete is showing
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
