# ADR-0002: Native scrollback commit protocol

## Status

Accepted — 2026-09-13.

## Context

San draws inline. The managed frame — the live tail plus the input strip —
is the bottom rows of the terminal's primary screen; everything settled is
committed above it into the terminal's native scrollback with `tea.Println`.
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

1. **The frame a print runs under is frozen and painted before the insert.**
   `prepareScrollbackPrint` captures the frame into `frameForPrint`, `View`
   returns it until the print completes, and the fork's `insertAbove` flushes
   the queued view first. Measuring a frame the terminal does not show is
   exactly how a stale copy is born.

2. **A chunk is never taller than the room above the painted frame.**
   `capacity = H − h` in `flushState.prepareScrollbackPrint`; the queue is
   single-flight FIFO so no two inserts interleave.

3. **The frame never has zero rows.** When the real frame leaves no room
   above it, the print runs under `minimalScrollbackFrame` — one blank row —
   with `capacity = H − 1`. `insertAbove` positions relative to a frame row;
   a zero-row frame puts the block one row low and leaks a blank row into
   history.

4. **A shrinking inline frame is erased from its top row.** The fork's
   `flush` parks the cursor on row 0 before `Erase()` whenever the inline
   frame gets shorter, while the old height is still known. Invariant 3
   makes shrinking a routine step of every full-screen print, so this is
   load-bearing, not a corner.

5. **Nothing transient rides in the inline frame while a print is pending.**
   Overlays render in the alternate screen (#517); a print whose turn comes
   while an overlay owns the frame waits and restarts when it closes.

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
  Add a case there before changing any of the five sites.
- When something does appear twice, read the bytes before guessing:
  `TEA_TRACE=<file>` logs every renderer burst. The flush right before an
  `insert above` must reach the frame's top — `CR ESC[<k>A ESC[J`. A bare
  `CR ESC[J` means the erase started at the cursor, and the rows above it
  are about to become history.
- The renderer half lives in the `yanmxa/bubbletea` fork that `go.mod`
  replaces `charm.land/bubbletea/v2` with. Bumping upstream means carrying
  the `agent/*` patches forward; dropping one reopens its round of this
  family.
- Reducing how often a frame fills the screen — committing a tool result the
  moment it lands instead of showing it live first — would make invariant 3
  rare rather than routine. It is a separate change; the protocol above holds
  either way.

## References

- [`internal/app/model_scrollback.go`](../../../internal/app/model_scrollback.go)
  — the print queue, chunking, and the frozen frame.
- [`internal/app/model_scrollback_renderer_test.go`](../../../internal/app/model_scrollback_renderer_test.go)
  — the native-history gate.
- bubbletea fork branches `agent/flush-before-insert-above`,
  `agent/reflow-aware-resize-erase`, `agent/erase-from-frame-top-on-shrink`.
- charmbracelet/bubbletea#1736; genai-io/san#314, #497, #517.
