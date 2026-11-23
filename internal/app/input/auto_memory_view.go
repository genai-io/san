package input

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/setting"
)

type memoryPanelRow struct {
	label, detail, value string
	active               bool
}

func (p *autoMemoryPanel) Render(width, height int) string {
	width, height = max(width, 1), max(height, 1)
	muted := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	if !p.available {
		return muted.Render(kit.TruncateText("Settings unavailable", width))
	}
	if p.page == memoryEditPage {
		in := p.input
		in.SetWidth(max(1, width-2))
		lines := []string{in.View()}
		if p.err != nil && len(lines) < height {
			lines = append(lines, memoryPanelError(p.err, width))
		} else if height >= 3 {
			detail := "First Recall and Update request only. Leave blank to inherit."
			if p.option == memoryWaitTimeout {
				detail = "Only used in Wait mode. Leave blank to inherit."
			}
			lines = append(lines, "", muted.Render(kit.TruncateText(detail, width)))
		}
		return strings.Join(lines, "\n")
	}

	var rows []memoryPanelRow
	selected, heading := p.cursor, "Repository memory"
	if p.blocked {
		heading = "Paused · SAN_DISABLE_AUTO_MEMORY"
	} else if !p.enabled {
		heading = "Repository memory · Paused"
	}
	if p.page == memoryOptionsPage {
		selected, heading = p.option, "Repository settings"
		rows = []memoryPanelRow{
			{"Auto Memory", "Pause Recall and Update without resetting them", memoryState(p.enabled), p.enabled},
			{"Wait timeout", "Maximum wait before starting the main turn", p.recall.Timeout.String(), false},
			{"Preview lines", "First Aside request · line count", fmt.Sprint(p.preview.Lines), false},
			{"Preview size", "First Aside request · whichever limit comes first", fmt.Sprintf("%d KB", p.preview.KB), false},
		}
	} else {
		rows = []memoryPanelRow{
			{"Recall", "Find relevant notes for the next reply", recallModeLabel(p.recall.Mode), p.recall.Mode != setting.MemoryRecallOff},
			{"Update", "Automatically add, revise, or forget notes", memoryState(p.update), p.update},
		}
	}

	var lines []string
	budget := height
	if p.err != nil && budget > 1 {
		budget--
	}
	if budget > len(rows) || !p.enabled && p.page == memoryActionsPage && budget > 1 {
		lines = append(lines, muted.Render(kit.TruncateText(heading, width)))
		budget--
	}
	if budget > len(rows)+1 {
		lines = append(lines, "")
		budget--
	}
	visible := min(len(rows), budget)
	start := min(max(0, selected-visible+1), len(rows)-visible)
	wide := width >= 56
	for i := start; i < start+visible; i++ {
		lines = append(lines, renderMemoryPanelRow(rows[i], width, i == selected, wide))
	}
	if !wide && budget > visible {
		lines = append(lines, muted.Render(kit.TruncateText("  "+rows[selected].detail, width)))
	}
	if p.err != nil && len(lines) < height {
		lines = append(lines, memoryPanelError(p.err, width))
	}
	return strings.Join(lines, "\n")
}

func renderMemoryPanelRow(row memoryPanelRow, width int, selected, wide bool) string {
	muted := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	focus := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Focus).Bold(true)
	value := kit.TruncateText(row.value, max(0, width-4))
	valueWidth := lipgloss.Width(value)
	labelWidth := max(0, width-valueWidth-3)
	label := kit.TruncateText(row.label, labelWidth)
	prefix, name := "  ", label
	if selected {
		prefix, name = focus.Render("▸ "), focus.Render(label)
	}
	left := prefix + name
	if wide {
		gap := max(2, 14-lipgloss.Width(left))
		detailWidth := width - lipgloss.Width(left) - gap - valueWidth - 2
		if detailWidth > 0 {
			left += strings.Repeat(" ", gap) + muted.Render(kit.TruncateText(row.detail, detailWidth))
		}
	}
	style := muted
	if row.active {
		style = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Success)
	}
	return left + strings.Repeat(" ", max(0, width-lipgloss.Width(left)-valueWidth)) + style.Render(value)
}

func memoryState(on bool) string {
	if on {
		return "● On"
	}
	return "○ Off"
}

func memoryPanelError(err error, width int) string {
	return lipgloss.NewStyle().Foreground(kit.CurrentTheme.Error).Render(kit.TruncateText(err.Error(), width))
}
