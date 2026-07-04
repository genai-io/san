// Scrollback rendering: convert pending conversation messages into ANSI
// terminal output and emit them via tea.Println. The bubbletea alt-screen
// only paints the bottom input area; rendered messages live in the
// terminal's native scrollback above.
package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/core"
)

func (m *model) CommitMessages() []tea.Cmd {
	return m.renderAndCommit(true)
}

// blockRenderJob is the immutable snapshot the background render goroutine works
// from, so it never touches live model state.
type blockRenderJob struct {
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
}

// blocksRenderedMsg carries a finished background render back for
// handleBlocksRendered to commit to scrollback.
type blocksRenderedMsg struct {
	msgID                string
	index                int
	thinkingCommittedLen int
	contentCommittedLen  int
	thinkingEmitted      bool
	bulletEmitted        bool
	printed              string // "" when the blocks rendered empty (blank-only)
}

// FlushStreamingBlocks starts committing the streaming message's completed
// blocks to scrollback. The markdown render runs off the UI goroutine
// (renderBlocksCmd) so it can't stall repaint; one render runs at a time
// (flushRendering) to keep the Printlns ordered, and offsets advance only when
// it lands, so the live tail shows each block until its rendered form is in
// scrollback. Returns nil when a render is in flight or nothing new is ready.
func (m *model) FlushStreamingBlocks() []tea.Cmd {
	if m.flushRendering {
		return nil // a block is already rendering off-thread; wait for it to land
	}
	idx := len(m.conv.Messages) - 1
	if idx < 0 {
		return nil
	}
	msg := &m.conv.Messages[idx]
	if msg.Role != core.RoleAssistant {
		return nil
	}

	// Once content starts, flush thinking's trailing paragraph too (it has no
	// terminating blank line, but reasoning is done).
	thinkingEnd := conv.CompletedBlockBoundary(msg.Thinking)
	if len(msg.Content) > 0 {
		thinkingEnd = len(msg.Thinking)
	}
	contentEnd := conv.CompletedBlockBoundary(msg.Content)

	var thinkingSlice, contentSlice string
	if thinkingEnd > msg.ThinkingCommittedLen {
		thinkingSlice = msg.Thinking[msg.ThinkingCommittedLen:thinkingEnd]
	}
	if contentEnd > msg.ContentCommittedLen {
		contentSlice = msg.Content[msg.ContentCommittedLen:contentEnd]
	}
	if strings.TrimSpace(thinkingSlice) == "" && strings.TrimSpace(contentSlice) == "" {
		return nil // no completed block yet (or blank-only — nothing to render)
	}

	m.flushRendering = true
	return []tea.Cmd{renderBlocksCmd(blockRenderJob{
		msgID:            msg.ID,
		index:            idx,
		thinkingSlice:    thinkingSlice,
		contentSlice:     contentSlice,
		thinkingEnd:      thinkingEnd,
		contentEnd:       contentEnd,
		showThinkingIcon: !msg.ThinkingEmitted,
		showBullet:       !msg.BulletEmitted,
		width:            m.env.Width,
		md:               m.flushRenderer(),
	})}
}

// renderBlocksCmd renders the job's completed blocks (glamour, off the UI
// goroutine) and returns them as a blocksRenderedMsg.
func renderBlocksCmd(job blockRenderJob) tea.Cmd {
	return func() tea.Msg {
		// != "" just skips a slice absent this flush; the render helpers
		// blank-check their input and we gate on a non-empty result.
		var blocks []string
		thinkingEmitted := false
		if job.thinkingSlice != "" {
			if b := conv.RenderCommittedThinkingBlock(job.thinkingSlice, job.showThinkingIcon, job.width); b != "" {
				blocks = append(blocks, b)
				thinkingEmitted = true
			}
		}
		bulletEmitted := false
		if job.contentSlice != "" {
			if b := conv.RenderCommittedContentBlock(job.contentSlice, job.showBullet, job.md); b != "" {
				blocks = append(blocks, b)
				bulletEmitted = true
			}
		}
		printed := ""
		if len(blocks) > 0 {
			// Match RenderMessageAt's leading newline + blank-line separation.
			printed = "\n" + strings.Join(blocks, "\n\n")
		}
		return blocksRenderedMsg{
			msgID:                job.msgID,
			index:                job.index,
			thinkingCommittedLen: job.thinkingEnd,
			contentCommittedLen:  job.contentEnd,
			thinkingEmitted:      thinkingEmitted,
			bulletEmitted:        bulletEmitted,
			printed:              printed,
		}
	}
}

// handleBlocksRendered lands a background render: advance the row's offsets,
// print to scrollback, then flush the next completed block.
func (m *model) handleBlocksRendered(msg blocksRenderedMsg) tea.Cmd {
	m.flushRendering = false

	// Drop the render if its row was committed whole (turn-end/cancel) or
	// replaced by a retry (new ID) meanwhile — its content is already handled, so
	// printing it now would duplicate or reorder scrollback.
	if msg.index >= len(m.conv.Messages) ||
		msg.index < m.conv.CommittedCount ||
		m.conv.Messages[msg.index].ID != msg.msgID ||
		m.conv.Messages[msg.index].Role != core.RoleAssistant {
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
		cmds = append(cmds, tea.Println(msg.printed))
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

// flushRenderer is the background goroutine's own markdown renderer, kept off
// m.conv.MDRenderer so a slow render can't block the live View on its mutex.
// Rebuilt on width change; needs no lock since flushRendering means one render
// uses it at a time.
func (m *model) flushRenderer() *conv.MDRenderer {
	if m.flushMD == nil || m.flushMDWidth != m.env.Width {
		m.flushMD = conv.NewMDRenderer(m.env.Width)
		m.flushMDWidth = m.env.Width
	}
	return m.flushMD
}

func (m *model) commitAllMessages() []tea.Cmd {
	return m.renderAndCommit(false)
}

func (m *model) renderAndCommit(checkReady bool) []tea.Cmd {
	var parts []string
	lastIdx := len(m.conv.Messages) - 1
	params := m.messageRenderParams()

	for i := m.conv.CommittedCount; i < len(m.conv.Messages); i++ {
		msg := m.conv.Messages[i]

		if checkReady {
			if i == lastIdx && msg.Role == core.RoleAssistant && m.conv.Stream.Active {
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
	return []tea.Cmd{tea.Println(strings.Join(parts, "\n"))}
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
