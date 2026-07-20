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
	if len(items) == 0 {
		return ""
	}

	startIdx := 0
	if len(items) > maxVisibleQueueItems && selectedIdx >= maxVisibleQueueItems {
		startIdx = selectedIdx - maxVisibleQueueItems + 1
	}
	endIdx := min(startIdx+maxVisibleQueueItems, len(items))

	// The hint rides the row the eye is on: the selected row while editing,
	// otherwise the row nearest the input.
	hint := queueIdleHint
	hintIdx := endIdx - 1
	if selectedIdx >= 0 {
		hint = queueEditingHint
		hintIdx = selectedIdx
	}

	var sb strings.Builder
	for i := startIdx; i < endIdx; i++ {
		item := items[i]

		gutter := " "
		contentStyle := queueContentStyle
		if i == selectedIdx {
			gutter = kit.FocusBarStyle().Render(kit.FocusBar)
			contentStyle = queueSelectedContentStyle
		}

		// Budget: gutter(1) + prompt + right margin(2), minus the image tag
		// and hint when this row carries them.
		maxContentWidth := width - 3 - lipgloss.Width(InputPrompt)
		if item.HasImages {
			maxContentWidth -= lipgloss.Width("[Image] ")
		}
		if i == hintIdx {
			maxContentWidth -= lipgloss.Width(hint) + 2
		}

		content := contentStyle.Render(truncateQueueContent(item.Content, maxContentWidth))
		if item.HasImages {
			content = PendingImageStyle.Render("[Image] ") + content
		}

		line := gutter + queuePromptStyle.Render(InputPrompt) + content
		if i == hintIdx {
			if pad := width - lipgloss.Width(line) - lipgloss.Width(hint) - 1; pad >= 2 {
				line += strings.Repeat(" ", pad) + queueHintStyle.Render(hint)
			}
		}
		sb.WriteString(line + "\n")
	}

	if endIdx < len(items) {
		sb.WriteString(overflowStyle.Render(fmt.Sprintf("   +%d more below", len(items)-endIdx)) + "\n")
	}
	if startIdx > 0 {
		return overflowStyle.Render(fmt.Sprintf("   +%d more above", startIdx)) + "\n" + sb.String()
	}

	return sb.String()
}

// truncateQueueContent flattens a queued message to one line and truncates it
// to maxWidth display columns (CJK-aware).
func truncateQueueContent(s string, maxWidth int) string {
	s = strings.Join(strings.Fields(s), " ")
	if maxWidth < 8 {
		maxWidth = 8
	}
	return xansi.Truncate(s, maxWidth, "…")
}
