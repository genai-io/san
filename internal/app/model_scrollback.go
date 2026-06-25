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

// FlushStreamingBlocks commits the in-flight assistant message's newly-completed
// blocks to native scrollback, advancing its committed offsets so the live view
// and the turn-end commit render only the remainder. Both thinking and content
// commit block-by-block as blank lines (or, for content, closing code fences)
// land; once content starts — the reliable "reasoning done" signal — thinking's
// trailing block is flushed too so nothing reasoning-side lingers in the live
// view. Returns nil when nothing new is committable.
func (m *model) FlushStreamingBlocks() []tea.Cmd {
	idx := len(m.conv.Messages) - 1
	if idx < 0 {
		return nil
	}
	msg := &m.conv.Messages[idx]
	if msg.Role != core.RoleAssistant {
		return nil
	}

	var blocks []string

	// Thinking commits paragraph-by-paragraph as blank lines land; once content
	// starts, the trailing paragraph is flushed too (it has no terminating blank
	// line of its own).
	thinkingEnd := conv.CompletedBlockBoundary(msg.Thinking)
	if len(msg.Content) > 0 {
		thinkingEnd = len(msg.Thinking)
	}
	if thinkingEnd > msg.ThinkingCommittedLen {
		block := conv.RenderCommittedThinkingBlock(msg.Thinking[msg.ThinkingCommittedLen:thinkingEnd], !msg.ThinkingEmitted, m.env.Width)
		if block != "" {
			blocks = append(blocks, block)
			msg.ThinkingEmitted = true
		}
		msg.ThinkingCommittedLen = thinkingEnd
	}

	if boundary := conv.CompletedBlockBoundary(msg.Content); boundary > msg.ContentCommittedLen {
		block := conv.RenderCommittedContentBlock(msg.Content[msg.ContentCommittedLen:boundary], !msg.BulletEmitted, m.conv.MDRenderer)
		if block != "" {
			blocks = append(blocks, block)
			msg.BulletEmitted = true
		}
		msg.ContentCommittedLen = boundary
	}

	if len(blocks) == 0 {
		return nil
	}
	// Mirror RenderMessageAt's leading newline + blank-line block separation so
	// progressively-committed blocks land in scrollback exactly where the
	// turn-end render would place them.
	return []tea.Cmd{tea.Println("\n" + strings.Join(blocks, "\n\n"))}
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
