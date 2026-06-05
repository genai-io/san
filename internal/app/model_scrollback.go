// Scrollback rendering: convert pending conversation messages into ANSI
// terminal output and emit them via tea.Println. The bubbletea alt-screen
// only paints the bottom input area; rendered messages live in the
// terminal's native scrollback above.
package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/genai-io/san/v2/internal/app/conv"
	"github.com/genai-io/san/v2/internal/core"
)

func (m *model) CommitMessages() []tea.Cmd {
	return m.renderAndCommit(true)
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
		m.conv.CommittedCount = i + 1
	}

	if len(parts) == 0 {
		return nil
	}
	return []tea.Cmd{tea.Println(strings.Join(parts, "\n"))}
}
