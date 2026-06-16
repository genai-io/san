// Package conv: the pending-prompt queue preview shown above the input area,
// plus the compact "[N queued]" badge the status bar borrows.
package conv

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/kit"
)

// QueuePreviewItem is the minimal data needed to render a queue item preview.
type QueuePreviewItem struct {
	Content   string
	HasImages bool
}

var (
	queueBadgeStyle = lipgloss.NewStyle().
			Foreground(kit.CurrentTheme.Accent).
			Bold(true)

	queueContentStyle = lipgloss.NewStyle().
				Foreground(kit.CurrentTheme.TextDim)

	queueSelectedContentStyle = queueContentStyle.Foreground(kit.CurrentTheme.TextBright)

	queueSelectedBadgeStyle = lipgloss.NewStyle().
				Foreground(kit.CurrentTheme.TextBright).
				Bold(true)

	queueOverflowStyle = lipgloss.NewStyle().
				Foreground(kit.CurrentTheme.Muted).
				Italic(true)
)

// RenderQueuePreview renders queued input items above the input area.
// selectedIdx is the currently selected item index (-1 = none).
func RenderQueuePreview(items []QueuePreviewItem, selectedIdx, width int) string {
	if len(items) == 0 {
		return ""
	}

	var sb strings.Builder

	maxVisible := 5
	startIdx := 0
	if len(items) > maxVisible && selectedIdx >= maxVisible {
		startIdx = selectedIdx - maxVisible + 1
	}
	endIdx := min(startIdx+maxVisible, len(items))

	for i := startIdx; i < endIdx; i++ {
		item := items[i]
		isSelected := i == selectedIdx

		content := truncateQueueContent(item.Content, width-8)
		if item.HasImages {
			content = PendingImageStyle.Render("[Image] ") + content
		}

		if isSelected {
			bar := kit.FocusBarStyle().Render(kit.FocusBar)
			num := queueSelectedBadgeStyle.Render(fmt.Sprintf("%d.", i+1))
			preview := queueSelectedContentStyle.Render(content)
			fmt.Fprintf(&sb, " %s %s %s\n", bar, num, preview)
		} else {
			badge := queueBadgeStyle.Render(fmt.Sprintf("  %d.", i+1))
			preview := queueContentStyle.Render(content)
			fmt.Fprintf(&sb, " %s %s\n", badge, preview)
		}
	}

	if len(items) > maxVisible {
		if endIdx < len(items) {
			sb.WriteString(queueOverflowStyle.Render(fmt.Sprintf("     +%d more below", len(items)-endIdx)) + "\n")
		}
		if startIdx > 0 {
			above := queueOverflowStyle.Render(fmt.Sprintf("     +%d more above", startIdx)) + "\n"
			return above + sb.String()
		}
	}

	return sb.String()
}

// renderQueueBadge renders a compact badge for the status bar.
func renderQueueBadge(count int) string {
	if count == 0 {
		return ""
	}
	return queueBadgeStyle.Render(fmt.Sprintf(" [%d queued]", count))
}

func truncateQueueContent(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")

	if maxLen <= 0 {
		maxLen = 40
	}

	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen-1]) + "…"
}
