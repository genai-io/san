package input

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/genai-io/san/internal/app/kit"
)

func pendingImageStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(kit.CurrentTheme.Primary)
}

func selectedImageStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(kit.CurrentTheme.TextBright).
		Background(kit.CurrentTheme.Primary).
		Bold(true)
}

// RenderTextarea renders the textarea with styled inline image tokens.
func (m *Model) RenderTextarea() string {
	view := strings.TrimRight(m.Textarea.View(), " ")
	if len(m.Images.Pending) == 0 {
		return view
	}

	selectedPendingIdx := -1
	if match, ok := m.SelectedImageMatch(); ok {
		selectedPendingIdx = match.PendingIdx
	}

	for _, match := range m.PendingImageMatches() {
		style := pendingImageStyle()
		if match.PendingIdx == selectedPendingIdx {
			style = selectedImageStyle()
		}
		view = strings.Replace(view, match.Label, style.Render(match.Label), 1)
	}

	return view
}
