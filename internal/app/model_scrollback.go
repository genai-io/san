// Scrollback rendering: convert pending conversation messages into ANSI
// terminal output. Recent settled rows stay above the composer; older rows
// enter the terminal's native scrollback by scrolling off the top of the frame,
// as output does in a plain terminal, or through tea.Println when the frame
// leaves room above it.
//
// The frame rule everything here obeys: the inline renderer only redraws the
// frame's current extent, so a row that leaves the screen stays exactly as it
// was last drawn. Scrollback cannot be rewritten. The full
// protocol — the invariants and the one site that enforces each — is
// docs/design/decisions/0002-native-scrollback-commit-protocol.md; read it
// before changing how a print measures, freezes, or shrinks the frame.
package app

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/setting"
)

type scrollbackPrintReadyMsg struct{ id uint64 }

type scrollbackPrintDoneMsg struct{ id uint64 }

type pendingScrollbackPrint struct {
	id        uint64
	remaining string
	rows      []uv.Line
	width     int // width rows and remaining are wrapped at; 0 until the first resize or chunk
	current   string
	frameRows int  // chat rows this print's content occupied in the live tail
	started   bool // first chunk prepared; the rest is no longer the frame's job
	scrolling bool // current is scrolling off the top of the frame (tea.ScrollUp in flight)
}

func printScrollback(id uint64) tea.Cmd {
	return func() tea.Msg {
		return scrollbackPrintReadyMsg{id: id}
	}
}

func (m *model) CommitMessages() []tea.Cmd {
	return m.renderAndCommit(true)
}

// Streaming-flush pipeline. As an assistant message streams in, each of its
// completed markdown blocks migrates from the live tail (redrawn every frame in
// the alt-screen) into the terminal's native scrollback, one block at a time:
//
//	FlushStreamingBlocks  freezes the newly-completed, not-yet-committed slice of
//	                      the live message into a flushSnapshot.
//	renderSnapshotCmd     renders that snapshot with glamour on a background Cmd,
//	                      so the parse + syntax highlight can't stall repaint.
//	flushResultMsg        carries the rendered ANSI back to the UI goroutine.
//	handleFlushResult     Println's it into scrollback and advances the message's
//	                      commit offsets, so the live tail stops redrawing the
//	                      prefix now committed and flushes the next block.
//
// One render is in flight at a time (flush.rendering) so the Printlns stay
// ordered; the still-streaming trailing block stays live until it too completes.

// flushSnapshot is the immutable snapshot the background render goroutine works
// from, so it never touches live model state.
type flushSnapshot struct {
	msgID            string
	index            int
	thinkingSlice    string
	contentSlice     string
	thinkingEnd      int // commit offsets, advanced once this render lands
	contentEnd       int
	showThinkingIcon bool
	showBullet       bool
	width            int
	md               *conv.MDRenderer
	// collapsedThinking is the collapsed mode's rendered "Thought for 3.2 s"
	// line, committed instead of a reasoning body. thinkingSlice is empty in
	// that case: the body never reaches scrollback in the collapsed and hidden
	// modes.
	collapsedThinking string
}

// flushState is the streaming-block flush subsystem: it renders each completed
// conversation block (thinking, content) off the UI goroutine and commits the
// result to scrollback. See FlushStreamingBlocks and model_scrollback.go.
type flushState struct {
	rendering       bool                     // one render in flight at a time, so Printlns stay ordered
	renderer        *conv.MDRenderer         // background renderer, off the live-view MDRenderer's mutex
	width           int                      // width the renderer was built for; rebuild when it changes
	nextPrintID     uint64                   // monotonic identity for queued scrollback prints
	pendingPrints   []pendingScrollbackPrint // FIFO queue; only the head may be in flight
	frameForPrint   *tea.View                // freeze insertAbove geometry until the print completes
	visible         []uv.Line                // unwrapped settled lines not yet in native scrollback
	clearAfterPrint tea.Cmd                  // wipe only after an already-issued Println finishes
	output          *terminalOutput          // redraw and history insertion reach the terminal together
	resizedFrom     int                      // width of the frame a resize left on screen, until the terminal reports what it scrolled off
	unscrolled      []string                 // rows a resize kept from scrolling into history, printed again before the rest
}

// flushResultMsg is the result of rendering a flushSnapshot off-thread, carrying
// the rendered blocks back for handleFlushResult to commit to scrollback.
type flushResultMsg struct {
	msgID                string
	index                int
	thinkingCommittedLen int
	contentCommittedLen  int
	thinkingEmitted      bool
	bulletEmitted        bool
	printed              string // "" when the blocks rendered empty (blank-only)
}

// FlushStreamingBlocks starts one turn of the streaming-flush pipeline above:
// it freezes the inference message's completed blocks into a flushSnapshot and
// kicks off their background render. Returns nil when a render is already in
// flight or nothing new is ready to flush.
func (m *model) FlushStreamingBlocks() []tea.Cmd {
	if m.flush.rendering {
		return nil // a block is already rendering off-thread; wait for it to land
	}
	idx := m.conv.StreamMessageIndex()
	if idx < 0 {
		return nil
	}
	msg := &m.conv.Messages[idx]

	// A text delta finishes the current reasoning paragraph, even without a
	// trailing blank line. Earlier commentary does not: Responses may return
	// to thinking within the same message, and that new paragraph must stay
	// live until it completes rather than committing each token on its own.
	reasoningDone := len(msg.Content) > 0 && !m.conv.Stream.ThinkingActive
	var thinkingSlice, contentSlice, collapsedThinking string
	thinkingEnd := msg.ThinkingCommittedLen
	if setting.DrawsThinkingBody(m.env.ThinkingDisplay) {
		thinkingEnd = conv.CompletedBlockBoundary(msg.Thinking)
		if reasoningDone {
			thinkingEnd = len(msg.Thinking)
		}
		if thinkingEnd > msg.ThinkingCommittedLen {
			thinkingSlice = msg.Thinking[msg.ThinkingCommittedLen:thinkingEnd]
		}
	} else if reasoningDone && !msg.ThinkingEmitted && len(msg.Thinking) > 0 {
		// Collapsed/hidden: the body must never reach the screen, so nothing is
		// sliced for commit. The offsets still advance past it, and collapsed
		// commits one "Thought for 3.2 s" line instead — which is why this
		// waits for reasoningDone rather than the first completed block.
		thinkingEnd = len(msg.Thinking)
		if m.env.ThinkingDisplay == setting.ThinkingDisplayCollapsed {
			collapsedThinking = conv.RenderCollapsedThinking(msg.ThinkingDuration)
		}
	}
	contentEnd := conv.CompletedBlockBoundary(msg.Content)
	if contentEnd > msg.ContentCommittedLen {
		contentSlice = msg.Content[msg.ContentCommittedLen:contentEnd]
	}
	if strings.TrimSpace(thinkingSlice) == "" && strings.TrimSpace(contentSlice) == "" && collapsedThinking == "" {
		return nil // no completed block yet (or blank-only — nothing to render)
	}

	m.flush.rendering = true
	return []tea.Cmd{renderSnapshotCmd(flushSnapshot{
		msgID:             msg.ID,
		index:             idx,
		thinkingSlice:     thinkingSlice,
		contentSlice:      contentSlice,
		thinkingEnd:       thinkingEnd,
		contentEnd:        contentEnd,
		showThinkingIcon:  !msg.ThinkingEmitted,
		showBullet:        !msg.BulletEmitted,
		width:             m.env.Width,
		md:                m.flush.mdRenderer(m.env.Width),
		collapsedThinking: collapsedThinking,
	})}
}

// renderSnapshotCmd renders the snapshot's completed blocks (glamour, off the UI
// goroutine) and returns them as a flushResultMsg.
func renderSnapshotCmd(snap flushSnapshot) tea.Cmd {
	return func() tea.Msg {
		// != "" just skips a slice absent this flush; the render helpers
		// blank-check their input and we gate on a non-empty result.
		var blocks []string
		thinkingEmitted := false
		if snap.collapsedThinking != "" {
			blocks = append(blocks, snap.collapsedThinking)
			thinkingEmitted = true
		} else if snap.thinkingSlice != "" {
			if b := conv.RenderCommittedThinkingBlock(snap.thinkingSlice, snap.showThinkingIcon, snap.width, snap.md); b != "" {
				blocks = append(blocks, b)
				thinkingEmitted = true
			}
		}
		bulletEmitted := false
		if snap.contentSlice != "" {
			if b := conv.RenderCommittedContentBlock(snap.contentSlice, snap.showBullet, snap.md); b != "" {
				blocks = append(blocks, b)
				bulletEmitted = true
			}
		}
		printed := ""
		if len(blocks) > 0 {
			// Match RenderMessageAt's leading newline + blank-line separation.
			printed = "\n" + strings.Join(blocks, "\n\n")
		}
		return flushResultMsg{
			msgID:                snap.msgID,
			index:                snap.index,
			thinkingCommittedLen: snap.thinkingEnd,
			contentCommittedLen:  snap.contentEnd,
			thinkingEmitted:      thinkingEmitted,
			bulletEmitted:        bulletEmitted,
			printed:              printed,
		}
	}
}

// handleFlushResult lands a background render: advance the row's offsets,
// print to scrollback, then flush the next completed block.
func (m *model) handleFlushResult(msg flushResultMsg) tea.Cmd {
	m.flush.rendering = false

	// Drop the render if its row was committed whole (turn-end/cancel) or
	// replaced by a retry (new ID) meanwhile — its content is already handled, so
	// printing it now would duplicate or reorder scrollback.
	if msg.index >= len(m.conv.Messages) ||
		msg.index < m.conv.CommittedCount ||
		m.conv.Messages[msg.index].ID != msg.msgID ||
		m.conv.Messages[msg.index].Role != core.ChatAssistant {
		return nil
	}

	row := &m.conv.Messages[msg.index]
	row.ThinkingCommittedLen = msg.thinkingCommittedLen
	row.ContentCommittedLen = msg.contentCommittedLen
	if msg.thinkingEmitted {
		row.ThinkingEmitted = true
	}
	if msg.bulletEmitted {
		row.BulletEmitted = true
	}

	var cmds []tea.Cmd
	if msg.printed != "" {
		// No budget: a streamed block leaves the tail as its rendered self.
		cmds = append(cmds, m.queueScrollbackPrint(msg.printed, 0))
	}
	// Catch a block that completed while this one rendered — Stream.Active means
	// the row is still uncommitted, so it's safe.
	if m.conv.Stream.Active {
		cmds = append(cmds, m.FlushStreamingBlocks()...)
	}
	// Sequence, not Batch: this block's print must be queued before the next
	// render's result, or concurrent Batch could reorder scrollback blocks.
	return tea.Sequence(cmds...)
}

// mdRenderer returns the background goroutine's own markdown renderer, kept off
// m.conv.MDRenderer so a slow render can't block the live View on its mutex.
// Rebuilt on width change; needs no lock since flush.rendering means one render
// uses it at a time.
func (f *flushState) mdRenderer(width int) *conv.MDRenderer {
	if f.renderer == nil || f.width != width {
		f.renderer = conv.NewMDRenderer(width)
		f.width = width
	}
	return f.renderer
}

func (m *model) commitAllMessages() []tea.Cmd {
	return m.renderAndCommit(false)
}

func (m *model) renderAndCommit(checkReady bool) []tea.Cmd {
	var parts []string
	assistantIdx := m.conv.StreamMessageIndex()
	params := m.messageRenderParams()
	// What the loop below is about to take out of the frame.
	preCommitRows := rowCount(conv.RenderActiveContent(params))

	for i := m.conv.CommittedCount; i < len(m.conv.Messages); i++ {
		msg := m.conv.Messages[i]

		if checkReady {
			if i == assistantIdx && m.conv.Stream.Active {
				break
			}
			// Keep calls live until their whole batch can be printed with its results.
			if msg.Role == core.ChatAssistant && len(msg.ToolCalls) > 0 && len(m.conv.Tool.PendingCalls) > 0 {
				break
			}
		}

		if rendered := conv.RenderSingleMessage(params, i); rendered != "" {
			parts = append(parts, rendered)
		}
		// Fully in scrollback now (any progressively-flushed prefix plus this
		// remainder). Clear the commit offsets so a later full rebuild (resize
		// reflow, compact reprint) renders the message whole, not just its tail.
		m.conv.Messages[i].ResetStreamCommit()
		m.conv.CommittedCount = i + 1
	}

	if len(parts) == 0 {
		return nil
	}
	if banner := m.takeWelcomeBanner(); banner != "" {
		parts = append([]string{banner}, parts...)
	}
	return []tea.Cmd{m.queueScrollbackPrint(strings.Join(parts, "\n"), preCommitRows)}
}

// Keep the latest settled rows visible; only overflow enters immutable history.
func (m *model) queueScrollbackPrint(content string, frameRows int) tea.Cmd {
	if !m.env.Ready || m.env.Height <= 0 {
		return m.flush.queueScrollbackPrint(content, frameRows)
	}
	m.flush.visible = append(m.flush.visible, scrollbackPhysicalLines(content, 0)...)
	for i, line := range m.flush.visible {
		m.flush.visible[i] = trimPadding(line)
	}
	return m.flushVisibleOverflow()
}

// Layout changes must hand settled rows to history before the view crops them.
func (m *model) flushVisibleOverflow() tea.Cmd {
	if !m.env.Ready || m.env.Height <= 0 || len(m.flush.visible) == 0 || m.flush.resizedFrom > 0 {
		return nil
	}
	bottom, params := m.frameBottom()
	keep := max(0, m.env.Height-rowCount(bottom)-rowCount(conv.RenderActiveContent(params)))
	rows := wrapScrollbackLines(m.flush.visible, m.env.Width)
	overflow := len(rows) - keep
	if overflow <= 0 {
		return nil
	}
	m.flush.dropVisibleRows(rows[:overflow])
	printed := renderScrollbackLines(rows[:overflow])
	return m.flush.queueScrollbackPrint(printed, 0)
}

// dropVisibleRows takes rows, the leading wrapped rows of visible, off it.
func (f *flushState) dropVisibleRows(rows []uv.Line) {
	for _, row := range rows {
		f.visible[0] = f.visible[0][len(row):]
		if len(f.visible[0]) == 0 {
			f.visible = f.visible[1:]
		}
	}
	if len(f.visible) > 0 {
		f.visible[0] = slices.Clone(f.visible[0])
	}
	f.visible = slices.Clone(f.visible)
}

// takeResizeScrollback forgets the settled rows a resize had the terminal push
// into its own scrollback, so committing the overflow after it does not repeat
// them. They were drawn at the width before the resize, at the top of the frame;
// a row the report does not match leaves visible as it is.
func (f *flushState) takeResizeScrollback(msg tea.ResizeScrollbackMsg) {
	drawnWidth := f.resizedFrom
	f.resizedFrom = 0
	f.unscrolled = append(f.unscrolled, msg.Unscrolled...)
	if !msg.Known || len(msg.Lines) == 0 || len(f.pendingPrints) > 0 {
		return
	}
	rows := wrapScrollbackLines(f.visible, drawnWidth)
	n := min(len(msg.Lines), len(rows))
	for i := range n {
		if !sameRow(renderScrollbackLines([]uv.Line{rows[i]}), msg.Lines[i]) {
			return
		}
	}
	f.dropVisibleRows(rows[:n])
}

// requeueUnscrolled puts the rows a resize kept from scrolling into history
// ahead of everything else owed to it, at width. The head must have no chunk in
// flight.
func (f *flushState) requeueUnscrolled(width int) tea.Cmd {
	if len(f.unscrolled) == 0 {
		return nil
	}
	rows := scrollbackPhysicalLines(strings.Join(f.unscrolled, "\n"), width)
	f.unscrolled = nil
	if len(f.pendingPrints) == 0 {
		return f.queueScrollbackPrint(renderScrollbackLines(rows), 0)
	}
	head := &f.pendingPrints[0]
	if head.remaining != "" {
		head.remaining = renderScrollbackLines(rows) + "\n" + head.remaining
		return nil
	}
	f.fitPendingRows(width)
	head.rows = append(rows, head.rows...)
	return nil
}

// fitPendingRows rewraps what is still queued for history at width: a handoff
// draws it in the frame, which has to stay within the terminal.
func (f *flushState) fitPendingRows(width int) {
	for i := range f.pendingPrints {
		p := &f.pendingPrints[i]
		if p.width == width {
			continue
		}
		if p.remaining != "" {
			p.remaining = renderScrollbackLines(scrollbackPhysicalLines(p.remaining, width))
		}
		if len(p.rows) > 0 {
			p.rows = scrollbackPhysicalLines(renderScrollbackLines(p.rows), width)
		}
		p.width = width
	}
}

// sameRow reports whether two renderings of a row show the same text.
func sameRow(a, b string) bool {
	return strings.TrimRight(ansi.Strip(a), " ") == strings.TrimRight(ansi.Strip(b), " ")
}

// startScrollbackPrint sends the queue head's next chunk to history. The frame
// spans the terminal, so there is no room above it: the chunk is drawn at the
// top of the chat rows and scrolled off. Only a frame that cannot scroll has the
// chunk inserted above it instead.
func (m *model) startScrollbackPrint(id uint64) tea.Cmd {
	if m.flush.resizedFrom > 0 {
		// Rows measured now could be for a frame the terminal is replacing;
		// the resize report restarts the queue.
		return nil
	}
	if !m.env.Ready || m.env.Height <= 0 {
		// No size yet, so nothing to fit: insert it all above what is drawn.
		content, ok := m.flush.prepareScrollbackPrint(id, m.env.Width, 0)
		if !ok {
			return nil
		}
		return m.insertChunk(id, content, m.View())
	}
	bottom, _ := m.frameBottom()
	rows := m.chatRows(bottom)
	if rows <= 0 {
		return m.insertChunkAboveOneRow(id)
	}
	content, ok := m.flush.prepareScrollbackPrint(id, m.env.Width, rows)
	if !ok {
		return nil
	}
	frame := m.View()
	if len(scrollbackPhysicalLines(frame.Content, m.env.Width)) != m.env.Height {
		// A row wider than the terminal makes the frame taller than it, and the
		// renderer drops rows off its top, so the chunk is not on the top row.
		return m.insertChunk(id, content, minimalScrollbackFrame())
	}
	m.flush.pendingPrints[0].scrolling = true
	m.flush.frameForPrint = &frame
	return tea.ScrollUp(rowCount(content), m.env.Width, m.env.Height)
}

// finishChunkScroll lands the renderer's answer to a chunk's scroll and starts
// the next chunk. A refusal wrote nothing; it comes from a resize, which can
// leave the chunk measured for a terminal that is gone, so the chunk is
// measured again and inserted above one row.
func (m *model) finishChunkScroll(msg tea.ScrollUpMsg) tea.Cmd {
	f := &m.flush
	if len(f.pendingPrints) == 0 || !f.pendingPrints[0].scrolling {
		return nil
	}
	head := &f.pendingPrints[0]
	f.frameForPrint = nil
	head.scrolling = false
	if !msg.Scrolled && head.current != "" {
		head.rows = append(scrollbackPhysicalLines(head.current, head.width), head.rows...)
		head.current = ""
		f.requeueUnscrolled(m.env.Width)
		return m.insertChunkAboveOneRow(head.id)
	}
	head.current = ""
	f.requeueUnscrolled(m.env.Width)
	if len(head.rows) > 0 || head.remaining != "" {
		return m.startScrollbackPrint(head.id)
	}
	return m.finishScrollbackPrint(head.id)
}

// insertChunk inserts content above frame, which is frozen until it lands.
func (m *model) insertChunk(id uint64, content string, frame tea.View) tea.Cmd {
	m.flush.frameForPrint = &frame
	if m.flush.output != nil {
		m.flush.output.Begin()
	}
	// Bubble Tea's renderer barrier flushes the latest managed View before
	// insertAbove. Sequence ensures completion is observed only after this
	// chunk has been inserted.
	return tea.Sequence(
		tea.Println(content),
		func() tea.Msg { return scrollbackPrintDoneMsg{id: id} },
	)
}

// insertChunkAboveOneRow inserts the next chunk above minimalScrollbackFrame,
// which leaves all but one row of the terminal above it.
func (m *model) insertChunkAboveOneRow(id uint64) tea.Cmd {
	if m.flush.resizedFrom > 0 {
		return nil // see startScrollbackPrint
	}
	capacity := max(1, m.env.Height-minimalScrollbackFrameHeight)
	content, ok := m.flush.prepareScrollbackPrint(id, m.env.Width, capacity)
	if !ok {
		return nil
	}
	return m.insertChunk(id, content, minimalScrollbackFrame())
}

// frameBottom is the block anchored under the chat rows — indicators plus the
// composer, or the docked modal in its place — with the render params that
// layout implies.
func (m *model) frameBottom() (string, conv.RenderContext) {
	footer, _ := m.renderFooter("")
	bottom := footer
	params := m.messageRenderParams()
	if ov, active := m.activeOverlay(); active && isDockedModal(ov) {
		bottom = "\n\n" + ov.Render()
		params.DockedModalActive = true
	}
	return m.renderChatIndicators(m.renderTrackerList(), bottom) + bottom, params
}

// Split at cell boundaries without turning soft wraps into stored newlines.
func wrapScrollbackLines(lines []uv.Line, width int) []uv.Line {
	if width <= 0 {
		return slices.Clone(lines)
	}
	var rows []uv.Line
	for _, line := range lines {
		start, columns := 0, 0
		for i, cell := range line {
			if columns+cell.Width > width && i > start {
				rows = append(rows, line[start:i])
				start, columns = i, 0
			}
			columns += cell.Width
		}
		rows = append(rows, line[start:])
	}
	return rows
}

// resumeDeferredScrollbackPrint restarts the queue once no panel owns the frame.
//
// Update calls it on the overlay-closed edge rather than on the three prompt
// replies: the queue is single-flight, so a head nobody restarts stalls every
// block behind it for the session, and renderAndCommit advances CommittedCount
// before the print runs — the live tail has already stopped drawing what the
// queue is still holding, which makes a stall a disappearance.
func (m *model) resumeDeferredScrollbackPrint() tea.Cmd {
	if len(m.flush.pendingPrints) == 0 || m.flush.pendingPrints[0].current != "" || m.flush.pendingPrints[0].scrolling {
		return nil
	}
	return printScrollback(m.flush.pendingPrints[0].id)
}

// queueScrollbackPrint appends content to a single-flight FIFO. Only an empty
// queue starts a print; finishScrollbackPrint starts the next chunk after Bubble
// Tea has processed the current Println. Each chunk is no taller than the rows
// above the managed frame, so insertAbove cannot scroll live UI rows into native
// history.
func (f *flushState) queueScrollbackPrint(content string, frameRows int) tea.Cmd {
	if content == "" {
		return nil
	}
	f.nextPrintID++
	pending := pendingScrollbackPrint{
		id:        f.nextPrintID,
		remaining: content,
		frameRows: frameRows,
	}
	f.pendingPrints = append(f.pendingPrints, pending)
	if len(f.pendingPrints) > 1 {
		return nil
	}
	return printScrollback(pending.id)
}

// finishScrollbackPrint completes only the in-flight queue head. A stale or
// out-of-order done message is ignored; the next chunk or queued print cannot
// start until the current Println has been processed.
func (m *model) finishScrollbackPrint(id uint64) tea.Cmd {
	if len(m.flush.pendingPrints) > 0 && m.flush.pendingPrints[0].id == id && m.flush.output != nil {
		if err := m.flush.output.Flush(); err != nil {
			return tea.Quit
		}
	}
	next := m.flush.finishScrollbackPrint(id)
	if queued := m.flush.requeueUnscrolled(m.env.Width); queued != nil {
		next = tea.Batch(next, queued)
	}
	return next
}

func (m *model) clearScrollback(wipe tea.Cmd) tea.Cmd {
	f := &m.flush
	f.visible = nil
	f.unscrolled = nil
	if len(f.pendingPrints) > 0 && f.pendingPrints[0].current != "" {
		// Clear canceled display rows while keeping room for the issued insert.
		m.useMinimalScrollbackFrame()
		f.pendingPrints = slices.Clone(f.pendingPrints[:1])
		f.pendingPrints[0].remaining = ""
		f.pendingPrints[0].rows = nil
		if f.pendingPrints[0].scrolling {
			// Whether it scrolls or not, the wipe that follows erases it.
			f.pendingPrints[0].current = ""
		}
		f.clearAfterPrint = wipe
		return nil
	}
	f.pendingPrints = nil
	f.frameForPrint = nil
	return wipe
}

func (f *flushState) finishScrollbackPrint(id uint64) tea.Cmd {
	if len(f.pendingPrints) == 0 || f.pendingPrints[0].id != id {
		return nil
	}
	f.frameForPrint = nil
	f.pendingPrints[0].current = ""
	f.pendingPrints[0].scrolling = false
	var next tea.Cmd
	if f.pendingPrints[0].remaining != "" || len(f.pendingPrints[0].rows) > 0 {
		next = printScrollback(id)
	} else {
		f.pendingPrints = f.pendingPrints[1:]
		if len(f.pendingPrints) > 0 {
			next = printScrollback(f.pendingPrints[0].id)
		}
	}
	wipe := f.clearAfterPrint
	f.clearAfterPrint = nil
	return tea.Sequence(wipe, next)
}

// minimalScrollbackFrame is the one-row frame a chunk is inserted above when the
// frame cannot scroll, or that replaces a frozen frame a resize or clear has
// invalidated. One row, not zero: a zero-height frame makes the renderer's
// inline erase start at the cursor's row instead of the frame's top row, so the rows of the previously painted frame above the cursor
// survive, and the print's scroll then pushes them into native history as a
// stale copy.
func minimalScrollbackFrame() tea.View {
	v := tea.NewView(" ")
	v.ReportFocus = true // as View does, or each print toggles focus reporting
	return v
}

// minimalScrollbackFrameHeight is the rows minimalScrollbackFrame occupies.
const minimalScrollbackFrameHeight = 1

// prepareScrollbackPrint takes the queue head's next chunk of at most capacity
// rows (all of them when capacity is not positive) as the print in flight.
func (f *flushState) prepareScrollbackPrint(id uint64, width, capacity int) (string, bool) {
	if len(f.pendingPrints) == 0 || f.pendingPrints[0].id != id || f.pendingPrints[0].current != "" {
		return "", false
	}
	head := &f.pendingPrints[0]
	if head.remaining != "" {
		head.rows = scrollbackPhysicalLines(head.remaining, width)
		head.remaining = ""
		head.width = width
	} else if len(head.rows) > 0 && head.width != width {
		head.rows = scrollbackPhysicalLines(renderScrollbackLines(head.rows), width)
		head.width = width
	}
	lines := head.rows
	if len(lines) == 0 {
		return "", false
	}
	head.started = true

	if capacity <= 0 || capacity > len(lines) {
		capacity = len(lines)
	}
	head.current = renderScrollbackLines(lines[:capacity])
	clear(lines[:capacity])
	head.rows = lines[capacity:]
	return head.current, true
}

func (m *model) scrollbackFrameForPrint() (tea.View, bool) {
	if m.flush.frameForPrint == nil {
		return tea.View{}, false
	}
	return *m.flush.frameForPrint, true
}

func (m *model) useMinimalScrollbackFrame() {
	if m.flush.frameForPrint == nil {
		return
	}
	frame := minimalScrollbackFrame()
	m.flush.frameForPrint = &frame
}

// trimPadding drops the padding a full-width buffer leaves on a row.
// Free at the width it was printed at, but scrollback is immutable: narrow the
// window and text-plus-padding rewraps into two rows, the second all spaces.
//
// A trailing space counts as padding whatever foreground or bold it inherited —
// none of that shows on a space. Only a background or an underline does, and
// those are content.
func trimPadding(line uv.Line) uv.Line {
	end := len(line)
	for end > 0 && isPadding(&line[end-1]) {
		end--
	}
	return line[:end]
}

func isPadding(c *uv.Cell) bool {
	if c.IsZero() || c.Equal(&uv.EmptyCell) {
		return true
	}
	return c.Content == " " && c.Link.IsZero() &&
		c.Style.Bg == nil && c.Style.UnderlineColor == nil && c.Style.Underline == uv.UnderlineNone
}

// scrollbackPhysicalLines decomposes content exactly as Bubble Tea's
// insertAbove accounts for it: ANSI escapes are zero-width, grapheme clusters
// retain their terminal width, soft wraps consume rows, and a trailing newline
// creates a final blank row.
func scrollbackPhysicalLines(content string, width int) []uv.Line {
	if content == "" {
		return nil
	}
	height := len(strings.Split(content, "\n"))
	bufferWidth := width
	if bufferWidth <= 0 {
		bufferWidth = 1
	}
	for line := range strings.SplitSeq(content, "\n") {
		lineWidth := ansi.StringWidth(line)
		if width > 0 && lineWidth > width {
			height += lineWidth / width
		}
		if width <= 0 {
			bufferWidth = max(bufferWidth, lineWidth)
		}
	}

	buffer := uv.NewScreenBuffer(bufferWidth, height)
	buffer.Method = ansi.GraphemeWidth
	styled := uv.NewStyledString(content)
	styled.Wrap = true
	styled.Draw(buffer, buffer.Bounds())
	return buffer.Lines
}

func renderScrollbackLines(lines []uv.Line) string {
	if len(lines) == 0 {
		return ""
	}
	for i, line := range lines {
		lines[i] = trimPadding(line)
	}
	if rendered := uv.Lines(lines).Render(); rendered != "" {
		return rendered
	}
	// insertAbove ignores an empty string. A reset sequence represents one
	// physical blank row without adding visible content.
	return ansi.ResetStyle
}

// takeWelcomeBanner freezes the startup splash into scrollback once, on the
// first commit, then clears the pending flag so the live view (liveWelcome)
// stops drawing it. Freezing it here rather than before the TUI starts lets the
// banner capture the model the user selected after launch instead of freezing
// "no model selected" into scrollback.
func (m *model) takeWelcomeBanner() string {
	if !m.welcomePending {
		return ""
	}
	m.welcomePending = false
	return m.welcomeBannerText()
}

// welcomeBannerText renders the startup splash for the current model and cwd.
// It backs both the live banner (liveWelcome) and the scrollback freeze
// (takeWelcomeBanner) so the two always read identically.
func (m model) welcomeBannerText() string {
	return welcomeBanner(welcomeInfo{
		Model: m.env.GetModelDisplayName(),
		CWD:   m.env.CWD,
	})
}

// pendingScrollbackView is the handoff copy: the queued chunk stays drawn in the
// frame until its Println lands, so committing cannot shrink the frame mid-print.
//
// Restored from e02ed422, which 74ac1647 dropped as redundant beside the FIFO
// and the renderer's flush barrier. It is not: the barrier fixes *when*
// insertAbove sees the frame, this fixes *what* the frame is.
func (m *model) pendingScrollbackView() string {
	if len(m.flush.pendingPrints) == 0 {
		return ""
	}
	if m.handingOff() {
		return m.rowsToScrollOff()
	}
	// Until the first chunk goes out the whole payload stands in for the tail;
	// after it, only the chunk in flight. Holding the remainder would swell the
	// frame, and the next chunk's insertAbove welds that into its own output.
	head := m.flush.pendingPrints[0]
	handoff := head.current
	if !head.started {
		handoff = head.remaining
	}
	if handoff == "" {
		return ""
	}
	// The rendered block is shorter than the plain tail it replaces, and every
	// row of the difference is one the frame would vacate.
	return handoff + strings.Repeat("\n", max(head.frameRows-rowCount(handoff), 0))
}

// handingOff reports that settled rows are on their way into history. They
// leave by scrolling off the top, so the chat rows then read from the oldest of
// them instead of ending at the newest. An overlay holds the queue, and the chat
// keeps showing the newest rows under it.
func (m *model) handingOff() bool {
	if len(m.flush.pendingPrints) == 0 || !m.env.Ready || m.env.Height <= 0 {
		return false
	}
	_, held := m.activeOverlay()
	return !held
}

// rowsToScrollOff is the queue in reading order, at most a screen of it: the
// chunk in flight, the rest of its print, then the prints behind it.
func (m *model) rowsToScrollOff() string {
	budget := m.env.Height
	var parts []string
	add := func(s string) {
		if s != "" && budget > 0 {
			s = headLines(s, budget)
			budget -= rowCount(s)
			parts = append(parts, s)
		}
	}
	for _, p := range m.flush.pendingPrints {
		add(p.current)
		if len(p.rows) > 0 && budget > 0 {
			add(renderScrollbackLines(slices.Clone(p.rows[:min(len(p.rows), budget)])))
		}
		add(p.remaining)
	}
	return strings.Join(parts, "\n")
}

// rowCount is how many terminal rows a rendered section occupies.
func rowCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}
