// Window resize handling. handleWindowResize runs the first time we get a
// window size (the deferred initial paint), where it commits any resumed
// conversation. On later width changes there is nothing to recompute: the live
// tail re-renders at the new width on the next frame, and already-committed
// scrollback is immutable to us — the terminal rewraps it on its own.
package app

import (
	tea "charm.land/bubbletea/v2"
)

func (m *model) handleWindowResize(msg tea.WindowSizeMsg) tea.Cmd {
	m.env.Width = msg.Width
	m.env.Height = msg.Height
	m.userInput.TerminalHeight = msg.Height
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
			if m.services.Session.GetStore() != nil {
				_ = m.userInput.Session.Selector.EnterSelect(m.env.Width, m.env.Height, m.services.Session.GetStore(), m.env.CWD)
			}
		}

		m.userInput.Textarea.SetWidth(msg.Width - 4 - 2)
		if len(cmds) > 0 {
			return tea.Batch(cmds...)
		}
		return nil
	}

	m.userInput.Textarea.SetWidth(msg.Width - 4 - 2)
	return nil
}
