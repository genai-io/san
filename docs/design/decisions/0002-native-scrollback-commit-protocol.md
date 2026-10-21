# ADR-0002: Native scrollback commit protocol

## Status

Accepted — 2026-09-13.

## Context

San draws inline. The managed frame — the live tail plus the input strip —
is the bottom rows of the terminal's primary screen. Recent settled rows remain
in a bounded display buffer above the bottom-fixed composer; older rows are
committed above the managed frame into native scrollback with `tea.Println`.
Native scrollback is immutable: every row that scrolls through the top of
the terminal stays there forever, and a row that should not have gone there
cannot be taken back.

`tea.Println` reaches scrollback through the renderer's `insertAbove`, whose
mechanics decide everything below. For a payload of `N` rows it moves to the
frame's bottom row, emits `N` line feeds, moves up `N + h − 1` rows (`h` is
the frame height), inserts `N` lines there, and writes the payload; the next
flush repaints the frame under it. The line feeds are what make room, and
once the frame sits at the bottom of the screen every one of them scrolls
one row out through the top. Those rows are history only if they were above
the frame, so:

> **A print of `N` rows is safe only while `N ≤ H − h`, where `H` is the
> terminal height and `h` is the height of the frame as it is painted on
> the terminal at that instant.** Anything else scrolls live rows into
> history.

The same bug family has been fixed five times, each at the site of its
symptom: the flush-before-insert patch (bubbletea#1736, fork branch
`agent/flush-before-insert-above`), the reflow-aware resize erase
(`agent/reflow-aware-resize-erase`), serialized prints (`74ac1647`), chunked
prints no taller than the room above the frame (`ce55709e`), and — the
trigger for this record — a tool result committed twice, once by the program
and once as a stale copy of the live frame. That one came from a shrink: the
renderer erased a shrinking inline frame from the cursor's row instead of the
frame's top row (ultraviolet's `move` clamps the tracked cursor row to the
new, smaller buffer height before the move that depends on the old one), the
old frame's upper rows survived on screen, and the print's line feeds pushed
them into history.

## Decision

The invariants, each with the one place that enforces it. A change that
touches frame geometry — the View's height, the frame a print runs under,
chunking, resize — keeps all of them or it is wrong.

1. **The frame a print runs under is frozen and painted before it runs.**
   `startScrollbackPrint` captures the frame into `frameForPrint`, `View`
   returns it until the chunk lands, and the fork's `insertAbove` and
   `scrollUp` flush the queued view first. Measuring a frame the terminal does not show is
   exactly how a stale copy is born.

2. **A frame that spans the terminal scrolls its chunk off the top.** The
   normal frame always fills the screen, so there is no room to insert above
   it. While a chunk is in flight the chat rows read from that chunk instead of
   from the newest row (`handingOff` in `chatTailAbove`): the chunk is drawn at
   the top, what follows it in reading order below, the composer where it was.
   `tea.ScrollUp` (fork) then emits line feeds at the bottom row and shifts the
   renderer's screen model, and those rows enter history exactly as they were
   drawn. Nothing is erased: the next frame paints only the rows that scrolled
   in. A chunk is at most the chat rows (`scrollChunkIntoHistory`).

3. **A scroll the renderer refuses is inserted above one row.** The fork
   refuses when the painted frame does not span the terminal, in the alternate
   screen, or while a resize is unreconciled, and then writes nothing. The
   chunk is put back and printed with `insertAbove` under
   `minimalScrollbackFrame`, `capacity = H − 1`; the same frame stands in when a
   frame row is wider than the terminal. One row, not zero: `insertAbove`
   positions relative to a frame row, and a zero-row frame leaks a blank row
   into history. A frame shorter than the terminal (before the first size is
   known) prints with `capacity = H − h`; the queue is single-flight FIFO so no
   two inserts interleave.

4. **A shrinking inline frame is erased from its top row.** Only the fallback
   in invariant 3 shrinks the frame. The fork once parked the cursor on row 0
   before `Erase()` for this; on bubbletea v2.1.0 with invariant 1 in place the
   park measured no difference, so it was dropped. A stale frame copy after a
   shrink means one of those broke first.

   The shrink is also what flickered: every shrink and regrow is a full erase
   and repaint, twice per committed row, visible in any terminal without
   synchronized output (tmux, Apple Terminal). Scrolling removed it.
   `terminalOutput` still holds a handoff's writes until its last chunk lands,
   so a synchronized terminal shows the whole handoff as one frame.

5. **Inline frames never move the cursor with a hard tab.** Painted rows
   become history, and terminals (tmux) keep a tab that moved the cursor as a
   tab, so code copied from history would change. The fork enables tab stops in
   the alternate screen only.

6. **Overlay prompts never enter a history handoff.**
   Full-screen panels render in the alternate screen (#517), where a print
   would vanish with the buffer; docked prompts (approval, question) render
   inline in the composer's place (#573), where insertAbove would carry the
   prompt into scrollback. Either way, a print whose turn comes while an
   overlay owns the frame waits and restarts when it closes.

7. **A resize redraw reaches the frame's top through the rows the terminal
   rewrapped.** The redraw moves up by the cursor's row *within* the frame,
   which is the physical distance only while every frame row is one terminal
   row; a narrowing rewraps the wide rows above the cursor, so the fork's
   `skipReflowedRows` first moves past the rows the rewrap added. Every
   terminal San targets rewraps — tmux has since 1.8 — so the only exemption
   is GNU screen, which truncates instead. A short move leaves the old
   frame's top rows on screen and each narrowing pushes the frame one row
   further down.

   Known limit: xterm.js (VS Code, Orca) does not keep the cursor on its
   logical row across a rewrap — every wide row *below* the cursor that
   splits pushes the cursor one row down, and the row it lands on is exempt
   from splitting. With the input strip's rule and status line below the
   prompt that is exactly one row per narrowing, so the redraw there still
   lands one row short. Nothing observable from the application separates
   this from a terminal that keeps the cursor put, short of asking the
   terminal where the cursor is after the resize; the count is not a
   heuristic to tune per terminal.

   A narrowing and shortening resize can also push the top of the old live
   frame into native history before San receives `WindowSizeMsg`. Redrawing
   cannot erase those rows without clearing real conversation history. The
   live content always follows the conversation, empty rows below it:
   moving it right above the input while a tool batch ran kept running status
   out of reach of such a resize, but shifted a short conversation down and
   back on every call — a flash on each fast one. A running call's status
   reaching history on a mid-call resize is the accepted cost. Arbitrarily small viewports
   can still force live content into history. vt10x does not model xterm
   resize reflow: validate such changes against xterm as well as the existing
   native-history gate.

Terminal behaviour that is *not* a San bug, so nobody "fixes" it again:
tmux with its default `scroll-on-clear on` treats an erase-below issued at
the home position as a clear and pushes the whole screen into history, so a
frame that fills the screen leaves a copy in tmux history on every full
repaint. xterm.js, Ghostty and iTerm2 never scroll on ED 0. Verify
scrollback behaviour with `tmux set scroll-on-clear off` or outside tmux.

## Consequences

- The gate is `internal/app/model_scrollback_renderer_test.go`: a vt10x
  terminal with `RecordHistory` drives the real renderer; a live-frame row,
  or a second copy of a committed row, anywhere in history fails the test.
  Add a case there before changing any of these sites.
- When something does appear twice, read the bytes before guessing:
  `TEA_TRACE=<file>` logs every renderer burst. A handoff's burst is line
  feeds then the rows that scrolled in, with no `ESC[J`. On the fallback, the
  flush right before an `insert above` must reach the frame's top —
  `CR ESC[<k>A ESC[J`. A bare
  `CR ESC[J` means the erase started at the cursor, and the rows above it
  are about to become history.
- The renderer half lives on the `san` branch of the `genai-io/bubbletea`
  fork that `go.mod` replaces `charm.land/bubbletea/v2` with. Bumping
  upstream means rebasing that branch; dropping one reopens its round of this
  family.
- The normal frame fills the screen to keep the composer stable. A short
  completed reply stays in the retained display buffer; only its oldest
  overflowing rows print. Each Update checks overflow against the space left
  by active content and bottom controls. Retained lines keep their original
  hard breaks; soft wraps are rebuilt at the current width, including after
  a partial line enters history. A handoff draws each chunk at the top of the
  full frame and scrolls it off; the composer and live activity stay put below
  it and never scroll.
  Queued payloads are parsed into physical rows once, then consumed without
  reparsing the remainder on every chunk. A width change rewraps only the
  remaining rows. `/clear` drops both unparsed payloads and cached rows.
- Activity is capped after reserving the composer or docked modal, with the
  same cap in the view and overflow check, so a tall workflow cannot displace
  the input cursor or modal options.
- `/clear` cancels retained rows and future print chunks. An issued chunk uses
  the minimal frame until completion (an issued scroll is let land), then the
  terminal wipe precedes new prints.
  Print IDs stay monotonic so late ready/done messages cannot affect a new queue.

## References

- [`internal/app/model_scrollback.go`](../../../internal/app/model_scrollback.go)
  — the print queue, chunking, and the frozen frame.
- [`internal/app/model_scrollback_renderer_test.go`](../../../internal/app/model_scrollback_renderer_test.go)
  — the native-history gate.
- `genai-io/bubbletea` branch `san`: the renderer patches on top of upstream.
- xterm.js `Buffer._reflowSmaller`: the cursor bump per split and the
  cursor-line exemption behind the known limit in invariant 7.
- charmbracelet/bubbletea#1736; genai-io/san#314, #497, #517.
