// Window resize handling. handleWindowResize runs the first time we get a
// window size (the deferred initial paint), where it commits any resumed
// conversation. The retained settled rows rewrap here; the active tail renders
// at the new width next frame. The terminal rewraps native scrollback itself.
package app

import (
	tea "charm.land/bubbletea/v2"
)

func (m *model) handleWindowResize(msg tea.WindowSizeMsg) tea.Cmd {
	if m.env.Ready && m.flush.resizedFrom == 0 {
		// The terminal may scroll some of the frame off as it resizes; overflow
		// waits for tea.ResizeScrollbackMsg to say which rows.
		m.flush.resizedFrom = m.env.Width
	}
	m.env.Width = msg.Width
	m.env.Height = msg.Height
	// A chunk prepared before this resize used the old terminal geometry. Keep
	// insertAbove safe by dropping the frozen managed frame; the payload may wrap
	// differently, but no live UI rows can then be scrolled into history.
	m.useMinimalScrollbackFrame()
	m.userInput.SetTerminalHeight(msg.Height)
	if ov, ok := m.activeOverlay(); ok {
		if resizable, ok := ov.(resizableOverlay); ok {
			resizable.Resize(msg.Width, msg.Height)
		}
	}

	m.conv.ResizeMDRenderer(msg.Width)

	if !m.env.Ready {
		m.env.Ready = true

		// Welcome banner is drawn before tea.NewProgram (see run.go); here
		// we only need to commit any resumed conversation.
		var cmds []tea.Cmd
		if len(m.conv.Messages) > 0 {
			cmds = append(cmds, m.commitAllMessages()...)
		}

		if m.userInput.Session.PendingSelector {
			m.userInput.Session.PendingSelector = false
			if m.services.SessionStore != nil {
				_ = m.userInput.Session.Selector.EnterSelect(m.env.Width, m.env.Height, m.services.SessionStore, m.env.CWD)
			}
		}

		m.userInput.Textarea.SetWidth(msg.Width - 4 - 2)
		if len(cmds) > 0 {
			return tea.Batch(cmds...)
		}
		return nil
	}

	m.userInput.Textarea.SetWidth(msg.Width - 4 - 2)
	m.flush.fitPendingRows(msg.Width)
	return nil
}

// finishResize lands the renderer's report of what the resize scrolled off and
// restarts the history queue it held.
func (m *model) finishResize(msg tea.ResizeScrollbackMsg) tea.Cmd {
	m.flush.takeResizeScrollback(msg)
	if len(m.flush.pendingPrints) > 0 && (m.flush.pendingPrints[0].current != "" || m.flush.pendingPrints[0].scrolling) {
		// The chunk in flight answers first and requeues the rows then.
		return nil
	}
	if queued := m.flush.requeueUnscrolled(m.env.Width); queued != nil {
		return queued
	}
	if _, active := m.activeOverlay(); active {
		return nil
	}
	return m.resumeDeferredScrollbackPrint()
}
