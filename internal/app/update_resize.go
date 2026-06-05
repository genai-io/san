// Window resize and scrollback reflow. handleWindowResize runs the first
// time we get a window size (the deferred initial paint), and whenever the
// terminal width changes — width changes invalidate previously-rendered
// scrollback, so we clear and reflow.
package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/genai-io/san/v2/internal/app/conv"
)

func (m *model) handleWindowResize(msg tea.WindowSizeMsg) tea.Cmd {
	oldWidth := m.env.Width
	m.env.Width = msg.Width
	m.env.Height = msg.Height
	m.userInput.TerminalHeight = msg.Height

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

	if oldWidth != msg.Width && m.conv.CommittedCount > 0 {
		return m.reflowScrollback()
	}

	return nil
}

func (m *model) reflowScrollback() tea.Cmd {
	committed := m.conv.CommittedCount
	m.conv.CommittedCount = 0

	var parts []string
	params := m.messageRenderParams()

	for i := range committed {
		if rendered := conv.RenderSingleMessage(params, i); rendered != "" {
			parts = append(parts, rendered)
		}
		m.conv.CommittedCount = i + 1
	}

	if len(parts) == 0 {
		return tea.ClearScreen
	}
	return tea.Sequence(tea.ClearScreen, tea.Println(strings.Join(parts, "\n")))
}
