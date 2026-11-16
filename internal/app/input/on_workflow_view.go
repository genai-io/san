package input

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/workflow"
)

func (v *WorkflowView) Render() string {
	if !v.active {
		return ""
	}
	title := "Workflow"
	var content string
	switch v.page {
	case "hub":
		title = "Workflow · definitions and recent runs"
		var lines []string
		lines = append(lines, "Saved definitions (Enter reviews before launch)")
		for i, d := range v.definitions {
			prefix := "  "
			if i == v.cursor {
				prefix = kit.FocusBarStyle().Render(kit.FocusBar) + " "
			}
			lines = append(lines, prefix+d.Name+" · "+d.Path)
		}
		lines = append(lines, "", "Recent runs")
		for i, r := range v.runs {
			prefix := "  "
			if len(v.definitions)+i == v.cursor {
				prefix = kit.FocusBarStyle().Render(kit.FocusBar) + " "
			}
			lines = append(lines, prefix+r.ID+" · "+r.Name+" · "+r.Status)
		}
		budget := max(1, v.height-7)
		if len(lines) > budget {
			index := 0
			for i, line := range lines {
				if strings.HasPrefix(ansi.Strip(line), kit.FocusBar+" ") {
					index = i
					break
				}
			}
			start := max(0, min(index-budget/2, len(lines)-budget))
			lines = lines[start : start+budget]
		}
		content = strings.Join(lines, "\n")
	case "review":
		title = "Review · " + v.name
		state := "Draft — save a valid definition before running"
		if v.saved {
			state = "Saved — r supplies inputs and launches"
		}
		content = state + "\nSave project workflow · " + v.name + "\nProject copy: saving does not modify a global/skill source.\n" + v.body.View()
	case "edit":
		title = "Edit source · " + v.name
		content = v.editor.View()
	case "inputs":
		title = "Run inputs · " + v.name
		if len(v.keys) == 0 {
			content = "No input placeholders. Enter launches this saved definition."
		} else {
			content = fmt.Sprintf("%s (%d/%d) — required\n", v.keys[v.field], v.field+1, len(v.keys)) + v.editor.View()
		}
	case "run", "output":
		if v.record == nil {
			content = "Run unavailable"
			break
		}
		elapsed := time.Since(v.record.StartedAt)
		if !v.record.FinishedAt.IsZero() {
			elapsed = v.record.FinishedAt.Sub(v.record.StartedAt)
		} else if v.record.Status != workflow.RunRunning {
			elapsed = max(0, v.record.UpdatedAt.Sub(v.record.StartedAt))
		}
		left := "Run · " + v.record.Name
		right := elapsed.Round(time.Second).String()
		title = left + strings.Repeat(" ", max(1, v.width-4-lipgloss.Width(left)-len(right))) + right
		if v.page == "output" {
			content = "Inspect · " + v.nodeID() + "\n" + v.body.View()
		} else {
			content = v.renderGraph() + "\n\n" + v.renderDetail()
		}
		if v.record.Status == workflow.RunFailed || v.record.Status == workflow.RunStopped || v.record.Status == workflow.RunInterrupted {
			content = "Run " + strings.ToUpper(v.record.Status) + "\n" + content
		}
		// Errors live outside the scrolling output viewport.
		if v.record.Error != "" {
			content = "Error: " + v.record.Error + "\n" + content
		}
		if c := v.record.Nodes[v.nodeID()]; c.Error != "" {
			content = "Node error: " + c.Error + "\n" + content
		}
	}
	if v.confirm != "" {
		switch v.confirm {
		case "save":
			title = "Confirm save · " + v.name
			content = "Save project workflow · " + v.name + "\n" + v.body.View() + "\nSave these changes? y / n"
		case "stop":
			content = "Stop this run? In-flight work may have side effects. y / n"
		case "discard":
			content = "Close and discard this unsaved draft? y / n"
		case "resume":
			action := "Resume unfinished work"
			if v.resumeFrom != "" {
				action = "Rerun from " + v.resumeFrom
			}
			content = v.body.View() + "\n" + action + "? y / n"
		}
	}
	if v.message != "" {
		content = v.message + "\n\n" + content
	}
	hint := "↑/↓ select · Enter open · Esc close"
	switch v.page {
	case "review":
		hint = "e edit · s preview/save · Esc close"
		if v.saved {
			hint = "r run · " + hint
		}
	case "edit":
		hint = "Ctrl+S validate and preview save · Esc review"
	case "inputs":
		hint = "Enter next / launch · Esc cancel"
	case "run":
		hint = "↑/↓ pin · f follow · Enter inspect · Esc close"
		if v.record != nil {
			switch v.record.Status {
			case workflow.RunRunning:
				hint = "s stop · " + hint
			case workflow.RunSucceeded:
				hint = "R rerun selected · " + hint
			default:
				hint = "r resume · R rerun · " + hint
			}
		}
	case "output":
		hint = "↑/↓ scroll · PgUp/PgDn · Home/End · Esc graph"
	}
	if v.confirm == "save" {
		hint = "y save · n cancel"
	}
	// Keep oversized source, activity and error strings inside the terminal frame.
	lines := strings.Split(content, "\n")
	limit := max(1, v.height-6)
	if len(lines) > limit {
		lines = lines[:limit]
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, max(1, v.width-2), "…")
	}
	separator := ansi.Truncate(kit.Panel{Width: v.width}.SeparatorLine(), v.width, "")
	return separator + "\n" + kit.SelectorTitleStyle().Render(ansi.Truncate(title, v.width, "…")) + "\n" + strings.Join(lines, "\n") + "\n" + separator + "\n" + ansi.Truncate(kit.HintLine(hint), v.width, "…")
}

func (v *WorkflowView) marker(id string) string {
	c := v.record.Nodes[id]
	mark := "○"
	if c.Status == workflow.StatusSucceeded || c.Status == workflow.StatusRunning {
		mark = "●"
	}
	if c.Status == workflow.StatusFailed {
		return lipgloss.NewStyle().Foreground(kit.CurrentTheme.Error).Render("●")
	}
	if v.record.Status == workflow.RunRunning && c.Status == workflow.StatusRunning {
		// The filled circle stays solid; theme brightness alone breathes.
		colors := []kit.AdaptiveColor{kit.CurrentTheme.Muted, kit.CurrentTheme.Accent, kit.CurrentTheme.Primary, kit.CurrentTheme.TextBright, kit.CurrentTheme.Primary, kit.CurrentTheme.Accent}
		return lipgloss.NewStyle().Foreground(colors[v.phase%len(colors)]).Render(mark)
	}
	return mark
}
func (v *WorkflowView) graphNode(id string) string {
	style := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Text)
	switch v.record.Nodes[id].Status {
	case "", workflow.StatusPending, workflow.StatusSkipped, workflow.StatusOmitted:
		style = style.Foreground(kit.CurrentTheme.TextDisabled)
	case workflow.StatusSucceeded:
		style = style.Foreground(kit.CurrentTheme.TextDim)
	}
	if id == v.nodeID() {
		style = kit.SelectorSelectedLabelStyle()
	}
	return " " + v.marker(id) + " " + style.Render(id) + " "
}

// Layers derive solely from the parsed topology, never checkpoint map iteration
// or node completion timing. Edges are shown explicitly, including joins.
func (v *WorkflowView) renderGraph() string {
	if v.plan == nil || v.record == nil {
		return "Graph unavailable"
	}
	levels := map[string]int{}
	for range v.plan.Nodes {
		changed := false
		for _, e := range v.plan.Edges {
			if levels[e.To] < levels[e.From]+1 {
				levels[e.To] = levels[e.From] + 1
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	maxLevel := 0
	for _, n := range v.plan.Nodes {
		maxLevel = max(maxLevel, levels[n.ID])
	}
	if v.width >= 80 && len(v.plan.Nodes) <= 10 {
		return v.renderDAG(levels, maxLevel)
	}
	return v.renderDependencyList(levels)
}
func (v *WorkflowView) renderDetail() string {
	id := v.nodeID()
	if id == "" {
		return ""
	}
	c := v.record.Nodes[id]
	elapsed := c.Duration
	if c.Status == workflow.StatusRunning && v.record.Status == workflow.RunRunning && !c.StartedAt.IsZero() {
		elapsed = time.Since(c.StartedAt)
	}
	detail := id
	switch c.Status {
	case workflow.StatusFailed:
		detail += " · Failed"
	case workflow.StatusSkipped:
		detail += " · Blocked"
	case workflow.StatusOmitted:
		detail += " · Not selected"
	}
	detail = kit.SelectorSelectedLabelStyle().Render(detail)
	if (!c.StartedAt.IsZero() || c.Duration > 0) && !(c.Status == workflow.StatusRunning && v.record.Status != workflow.RunRunning) {
		duration := elapsed.Round(time.Second).String()
		detail += strings.Repeat(" ", max(1, v.width-4-ansi.StringWidth(detail)-len(duration))) + lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted).Render(duration)
	}
	if v.record.Status != workflow.RunRunning {
		return detail + "\n\nResult\n" + c.Output
	}
	var tail []string
	for _, entry := range v.record.Activity[id] {
		tail = append(tail, strings.Split(entry, "\n")...)
	}
	start := max(0, len(tail)-3)
	for padding := len(tail); padding < 3; padding++ {
		detail += "\n"
	}
	for i := start; i < len(tail); i++ {
		line := tail[i]
		if i < len(tail)-1 {
			line = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted).Render(line)
		}
		detail += "\n" + line
	}
	if len(c.Workers) > 0 {
		detail += fmt.Sprintf("\n%d workers · Enter for output and full history", len(c.Workers))
	}
	return detail
}
func (v *WorkflowView) resumePreview() string {
	if v.record == nil || v.plan == nil {
		return "Run definition unavailable; recovery cannot be previewed"
	}
	states, err := workflow.Retry(v.plan, v.record.Nodes)
	if v.resumeFrom != "" {
		states, err = workflow.Invalidate(v.plan, v.record.Nodes, v.resumeFrom)
	}
	if err != nil {
		return err.Error()
	}
	lines := []string{"Resume preview — no automatic retry.", "Unknown/in-flight work may already have side effects; retry can duplicate them."}
	for _, n := range v.plan.Nodes {
		c := states[n.ID]
		action := "rerun"
		if c.Status == workflow.StatusSucceeded {
			action = "reuse successful result"
		} else if c.Status == workflow.StatusOmitted || c.Status == workflow.StatusSkipped {
			action = "re-evaluate dependencies and branch"
		}
		count := 0
		for _, w := range c.Workers {
			if w.State.Status == workflow.StatusSucceeded {
				count++
			}
		}
		if count > 0 && c.Status != workflow.StatusSucceeded {
			action += fmt.Sprintf("; reuse %d successful workers if plan unchanged", count)
		}
		lines = append(lines, n.ID+": "+action)
	}
	return strings.Join(lines, "\n")
}
