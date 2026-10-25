// Package conv: the pending-prompt queue preview rendered above the input
// separator. Queued messages draw as dim "❭" shadow prompts — already
// submitted, visually on their way into the conversation — with a key hint
// riding the active row.
package conv

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/app/kit"
)

// QueuePreviewItem is the minimal data needed to render a queue item preview.
type QueuePreviewItem struct {
	Content   string
	HasImages bool
}

const (
	maxVisibleQueueItems = 5
	queueIdleHint        = "↑ edit"
	queueEditingHint     = "enter done · ctrl+c delete"
)

var (
	queuePromptStyle = lipgloss.NewStyle().
				Foreground(kit.CurrentTheme.TextDim).
				Bold(true)

	queueContentStyle = lipgloss.NewStyle().
				Foreground(kit.CurrentTheme.TextDim)

	queueSelectedContentStyle = queueContentStyle.Foreground(kit.CurrentTheme.TextBright)

	queueHintStyle = lipgloss.NewStyle().
			Foreground(kit.CurrentTheme.Muted).
			Italic(true)

	// overflowStyle marks a "+N more" row — a count of what was clipped, not
	// content. Shared by every panel that windows its rows so the affordance
	// reads the same wherever it appears.
	overflowStyle = lipgloss.NewStyle().
			Foreground(kit.CurrentTheme.Muted).
			Italic(true)
)

// RenderQueuePreview renders queued messages as dim shadow prompts above the
// input separator, mirroring the "❭" of the live prompt below. selectedIdx is
// the item under edit (-1 = none); the selected row carries the focus bar and
// swaps the "↑ edit" affordance for the editing keys.
func RenderQueuePreview(items []QueuePreviewItem, selectedIdx, width int) string {
	return RenderQueuePreviewLimit(items, selectedIdx, width, maxVisibleQueueItems, true)
}

// RenderQueuePreviewLimit lets the composer surrender decoration and overflow
// rows before actionable queue content on short terminals. The edited item
// remains inside the visible window.
func RenderQueuePreviewLimit(items []QueuePreviewItem, selectedIdx, width, limit int, showOverflow bool) string {
	if len(items) == 0 || limit <= 0 {
		return ""
	}

	startIdx := 0
	if len(items) > limit && selectedIdx >= limit {
		startIdx = selectedIdx - limit + 1
	}
	endIdx := min(startIdx+limit, len(items))

	// The hint rides the row the eye is on: the selected row while editing,
	// otherwise the row nearest the input.
	hint := queueIdleHint
	hintIdx := len(items) - 1
	if selectedIdx >= 0 {
		hint = queueEditingHint
		hintIdx = selectedIdx
	}

	var sb strings.Builder
	for i := startIdx; i < endIdx; i++ {
		item := items[i]
		text := strings.Join(strings.Fields(item.Content), " ")

		gutter := " "
		contentStyle := queueContentStyle
		if i == selectedIdx {
			gutter = kit.FocusBarStyle().Render(kit.FocusBar)
			contentStyle = queueSelectedContentStyle
		}

		// Reserve the terminal's last column. Hints yield before message text.
		maxContentWidth := max(0, width-1-lipgloss.Width(gutter)-lipgloss.Width(InputPrompt))
		if item.HasImages {
			maxContentWidth = max(0, maxContentWidth-lipgloss.Width("[Image] "))
		}
		if i == hintIdx && maxContentWidth >= lipgloss.Width(text)+lipgloss.Width(hint)+2 {
			maxContentWidth -= lipgloss.Width(hint) + 2
		}

		if maxContentWidth <= 0 {
			text = ""
		} else {
			text = xansi.Truncate(text, maxContentWidth, "…")
		}
		content := contentStyle.Render(text)
		if item.HasImages {
			content = PendingImageStyle.Render("[Image] ") + content
		}

		line := gutter + queuePromptStyle.Render(InputPrompt) + content
		if i == hintIdx {
			if pad := width - lipgloss.Width(line) - lipgloss.Width(hint) - 1; pad >= 2 {
				line += strings.Repeat(" ", pad) + queueHintStyle.Render(hint)
			}
		}
		line = xansi.Truncate(line, max(0, width-1), "")
		sb.WriteString(line + "\n")
	}

	if showOverflow && endIdx < len(items) {
		sb.WriteString(overflowStyle.Render(fmt.Sprintf("   +%d more below", len(items)-endIdx)) + "\n")
	}
	if showOverflow && startIdx > 0 {
		return overflowStyle.Render(fmt.Sprintf("   +%d more above", startIdx)) + "\n" + sb.String()
	}

	return sb.String()
}
