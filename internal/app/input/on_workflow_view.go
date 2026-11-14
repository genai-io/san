package input

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/internal/workflow"
)

func (v *WorkflowView) Render() string {
	if !v.active {
		return ""
	}
	title, content, hint := v.renderPage()
	switch v.confirm {
	case "save":
		title = "Save · " + v.name
		content = v.body.View() + "\nSave this workflow in the project? y / n"
		hint = "y save · n cancel"
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
	if v.message != "" {
		content = v.message + "\n\n" + content
	}
	// Bound content here, including errors outside the scrollable viewport.
	lines := strings.Split(content, "\n")
	lines = lines[:min(len(lines), max(1, v.height-6))]
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, max(1, v.width-2), "…")
	}
	separator := ansi.Truncate(kit.Panel{Width: v.width}.SeparatorLine(), v.width, "")
	return separator + "\n" + kit.SelectorTitleStyle().Render(ansi.Truncate(title, v.width, "…")) + "\n" + strings.Join(lines, "\n") + "\n" + separator + "\n" + ansi.Truncate(kit.HintLine(hint), v.width, "…")
}

func (v *WorkflowView) renderPage() (title, content, hint string) {
	title, hint = "Workflow", "↑/↓ select · Enter open · Esc close"
	switch v.page {
	case "hub":
		title, content = "Workflow · definitions and recent runs", v.renderHub()
	case "review":
		title = "Review · " + v.name
		hint = "e edit · s save · Esc close"
		if v.saved {
			hint = "r run · " + hint
		} else {
			title += " · Unsaved"
		}
		for _, extra := range []string{"↑↓ step", "d limits", "PgUp/Dn scroll"} {
			if ansi.StringWidth(hint+" · "+extra) <= v.width {
				hint += " · " + extra
			}
		}
		content = v.body.View()
	case "details":
		title, content = "Execution limits · "+v.name, v.body.View()
		hint = "↑/↓ scroll · Esc review"
	case "edit":
		title = "Edit source · " + v.name
		content = v.templateHelp() + "\n\n" + v.editor.View()
		hint = "Ctrl+S preview save · Esc review"
	case "inputs":
		title, hint = "Run · "+v.name, "Enter next / run · Esc cancel"
		content = "No run inputs. Enter starts this workflow."
		if len(v.keys) > 0 {
			key := v.keys[v.field]
			users := strings.Join(v.referenceUsers("input."+key), ", ")
			content = fmt.Sprintf("%s · Required (%d/%d)\n{{input.%s}} takes the value below; used by %s.\n\n", key, v.field+1, len(v.keys), key, users) + v.editor.View()
		}
	case "run", "output":
		return v.renderRun()
	}
	return title, content, hint
}

func (v *WorkflowView) renderHub() string {
	lines := []string{"Saved definitions (Enter reviews before launch)"}
	for i, definition := range v.definitions {
		lines = append(lines, kit.RenderSelectableRow(definition.Name+" · "+definition.Path, i == v.cursor))
	}
	lines = append(lines, "", "Recent runs")
	for i, run := range v.runs {
		lines = append(lines, kit.RenderSelectableRow(run.ID+" · "+run.Name+" · "+run.Status, len(v.definitions)+i == v.cursor))
	}
	budget := max(1, v.height-7)
	if len(lines) > budget {
		selected := v.cursor + 1
		if v.cursor >= len(v.definitions) {
			selected += 2
		}
		start := max(0, min(selected-budget/2, len(lines)-budget))
		lines = lines[start : start+budget]
	}
	return strings.Join(lines, "\n")
}

func (v *WorkflowView) renderRun() (title, content, hint string) {
	title, hint = "Workflow", "↑/↓ pin · f follow · Enter inspect · Esc close"
	if v.record == nil {
		return title, "Run unavailable", hint
	}
	elapsed := time.Since(v.record.StartedAt)
	if !v.record.FinishedAt.IsZero() {
		elapsed = v.record.FinishedAt.Sub(v.record.StartedAt)
	} else if v.record.Status != workflow.RunRunning {
		elapsed = max(0, v.record.UpdatedAt.Sub(v.record.StartedAt))
	}
	left, right := "Run · "+v.record.Name, elapsed.Round(time.Second).String()
	title = left + strings.Repeat(" ", max(1, v.width-4-lipgloss.Width(left)-len(right))) + right
	if v.page == "output" {
		content = "Inspect · " + v.nodeID() + "\n" + v.body.View()
		hint = "↑/↓ scroll · PgUp/PgDn · Home/End · Esc graph"
	} else {
		content = v.renderGraph() + "\n\n" + v.renderDetail()
		switch v.record.Status {
		case workflow.RunRunning:
			hint = "s stop · " + hint
		case workflow.RunSucceeded:
			hint = "R rerun selected · " + hint
		default:
			hint = "r resume · R rerun · " + hint
		}
	}
	if v.record.Status == workflow.RunFailed || v.record.Status == workflow.RunStopped || v.record.Status == workflow.RunInterrupted {
		content = "Run " + strings.ToUpper(v.record.Status) + "\n" + content
	}
	if v.record.Error != "" {
		content = "Error: " + v.record.Error + "\n" + content
	}
	if node := v.record.Nodes[v.nodeID()]; node.Error != "" {
		content = "Node error: " + node.Error + "\n" + content
	}
	return title, content, hint
}

func (v *WorkflowView) renderReview() string {
	if v.plan == nil {
		return "Press e to fix the source before reviewing its inputs and outputs."
	}
	lines := []string{"Run inputs"}
	if keys := v.plan.InputNames(); len(keys) == 0 {
		lines = append(lines, "  No values to enter.")
	} else {
		for _, key := range keys {
			lines = append(lines, "  "+key+" — you provide this before running")
		}
	}
	lines = append(lines, "", "Steps")
	start := max(0, min(v.selected-2, len(v.plan.Nodes)-5))
	for i := start; i < min(len(v.plan.Nodes), start+5); i++ {
		node := v.plan.Nodes[i]
		label := "○ " + node.ID
		var upstream []string
		for _, edge := range node.Upstream() {
			name := edge.From
			if edge.Label != "" {
				name += " [" + edge.Label + "]"
			}
			upstream = append(upstream, name)
		}
		if len(upstream) > 0 {
			label += " ← " + strings.Join(upstream, ", ")
		}
		lines = append(lines, kit.RenderSelectableRow(label, i == v.selected))
	}
	node, ok := v.plan.Node(v.nodeID())
	if !ok {
		return strings.Join(lines, "\n")
	}
	lines = append(lines, "", node.ID)
	if node.ForEach != "" {
		lines = append(lines, "  Items   JSON array from "+node.ForEach)
	}
	if refs := node.References(); len(refs) == 0 {
		lines = append(lines, "  Input   No inserted values; uses the instructions below.")
	} else {
		for _, ref := range refs {
			origin := "final answer from step " + ref
			if key, ok := strings.CutPrefix(ref, "input."); ok {
				origin = key + " you enter before running"
			} else if ref == "item" || strings.HasPrefix(ref, "item.") {
				origin = "value from the current plan item"
			}
			lines = append(lines, "  Input   {{"+ref+"}} ← "+origin)
		}
	}
	output := "this step's final answer"
	if node.ForEach != "" {
		output = "combined answers from completed workers"
	}
	lines = append(lines, "  Output  {{"+node.Base+"}} = "+output)
	if users := v.referenceUsers(node.Base); len(users) > 0 {
		lines = append(lines, "          Read by "+strings.Join(users, ", "))
	} else if len(node.Downstream()) == 0 {
		lines = append(lines, "          Final workflow result")
	}
	lines = append(lines, "", "Instructions", "  "+strings.ReplaceAll(node.Prompt, "\n", "\n  "))
	return ansi.Wrap(strings.Join(lines, "\n"), max(1, v.width-4), " ")
}

func (v *WorkflowView) referenceUsers(ref string) []string {
	var users []string
	seen := map[string]bool{}
	if v.plan != nil {
		for _, node := range v.plan.Nodes {
			refs := node.References()
			if source, _, _ := strings.Cut(node.ForEach, "."); source != "" {
				refs = append(refs, source)
			}
			for _, name := range refs {
				if name == ref && !seen[node.Base] {
					users = append(users, node.Base)
					seen[node.Base] = true
				}
			}
		}
	}
	return users
}

func (v *WorkflowView) templateHelp() string {
	if v.plan == nil {
		return "{{input.name}} = a value you enter before running\n{{step}} = the final answer from that upstream step"
	}
	var inputs, outputs []string
	for _, key := range v.plan.InputNames() {
		inputs = append(inputs, "{{input."+key+"}}")
	}
	seen := map[string]bool{}
	for _, node := range v.plan.Nodes {
		if !seen[node.Base] {
			outputs = append(outputs, "{{"+node.Base+"}}")
			seen[node.Base] = true
		}
	}
	input := "No run inputs in this definition."
	if len(inputs) > 0 {
		input = strings.Join(inputs, ", ") + " = values you enter before running"
	}
	return input + "\n" + strings.Join(outputs, ", ") + " = step answers, available downstream"
}

func (v *WorkflowView) renderLimits() string {
	if v.plan == nil {
		return "Fix the source to inspect execution limits."
	}
	turns, bounds := v.plan.Bounds()
	lines := []string{
		"At most " + kit.Plural(turns, "step execution") + ". Each may use multiple model calls and tools.",
		"At most " + kit.Plural(v.plan.MaxParallel, "step") + " running at once; dependencies still determine their order.",
		"",
		"Each step starts in a fresh agent context. Run inputs and referenced step answers are inserted into its instructions.",
	}
	if len(bounds) > 0 {
		lines = append(lines, "", "Worker and retry limits")
		lines = append(lines, bounds...)
	}
	return ansi.Wrap(strings.Join(lines, "\n"), max(1, v.width-4), " ")
}

func (v *WorkflowView) diffPreview() string {
	if v.baseline == v.source {
		return "No source changes"
	}
	meta := perm.GenerateDiff(v.name+".md", v.baseline, v.source)
	if v.baseline == "" {
		meta = perm.GeneratePreview(v.name+".md", v.source, true)
	}
	out, _ := conv.RenderFileDiff(meta.Lines, max(1, v.width-4), 0)
	return out
}

func (v *WorkflowView) inspectOutput() string {
	if v.record == nil {
		return ""
	}
	node := v.record.Nodes[v.nodeID()]
	var out strings.Builder
	fmt.Fprintln(&out, node.Output)
	if activity := v.record.Activity[v.nodeID()]; len(activity) > 0 {
		fmt.Fprintln(&out, "\nActivity (retained live tail)")
		for _, line := range activity {
			fmt.Fprintln(&out, line)
		}
	}
	for _, attempt := range node.History {
		fmt.Fprintf(&out, "\nAttempt %d · %s\n%s\n%s\n", attempt.Number, attempt.Status, attempt.Output, attempt.Error)
	}
	for _, worker := range node.Workers {
		fmt.Fprintf(&out, "\nWorker %d %s · %s\n%s\n%s\n", worker.Index, worker.Label, worker.State.Status, worker.State.Output, worker.State.Error)
		for _, attempt := range worker.State.History {
			fmt.Fprintf(&out, "Attempt %d · %s\n%s\n%s\n", attempt.Number, attempt.Status, attempt.Output, attempt.Error)
		}
	}
	return out.String()
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
	for _, node := range v.plan.Nodes {
		state := states[node.ID]
		action := "rerun"
		if state.Status == workflow.StatusSucceeded {
			action = "reuse successful result"
		} else if state.Status == workflow.StatusOmitted || state.Status == workflow.StatusSkipped {
			action = "re-evaluate dependencies and branch"
		}
		finished := 0
		for _, worker := range state.Workers {
			if worker.State.Status == workflow.StatusSucceeded {
				finished++
			}
		}
		if finished > 0 && state.Status != workflow.StatusSucceeded {
			action += fmt.Sprintf("; reuse %d successful workers if plan unchanged", finished)
		}
		lines = append(lines, node.ID+": "+action)
	}
	return strings.Join(lines, "\n")
}

// Coordinates depend only on topology; completion and selection never reorder nodes.
func (v *WorkflowView) renderGraph() string {
	if v.plan == nil || v.record == nil {
		return "Graph unavailable"
	}
	levels := map[string]int{}
	for range v.plan.Nodes {
		changed := false
		for _, edge := range v.plan.Edges {
			if levels[edge.To] < levels[edge.From]+1 {
				levels[edge.To] = levels[edge.From] + 1
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	depth := 0
	for _, node := range v.plan.Nodes {
		depth = max(depth, levels[node.ID])
	}
	if v.width >= 80 && len(v.plan.Nodes) <= 10 {
		return v.renderDAG(levels, depth)
	}
	return v.renderDependencyList(levels)
}

func (v *WorkflowView) renderDAG(levels map[string]int, depth int) string {
	width := max(1, v.width-4)
	type position struct{ x, y int }
	positions := map[string]position{}
	rows := make([][]rune, depth*4+1)
	for i := range rows {
		rows[i] = []rune(strings.Repeat(" ", width))
	}
	for level := 0; level <= depth; level++ {
		var ids []string
		for _, node := range v.plan.Nodes {
			if levels[node.ID] == level {
				ids = append(ids, node.ID)
			}
		}
		slot := width / max(1, len(ids))
		for i, id := range ids {
			if len([]rune(id))+5 > slot {
				return v.renderDependencyList(levels)
			}
			positions[id] = position{min(width-1, slot*i+slot/2), level * 4}
		}
	}
	put := func(x, y int, r rune) {
		if y < 0 || y >= len(rows) || x < 0 || x >= width {
			return
		}
		if old := rows[y][x]; old != ' ' && old != r {
			rows[y][x] = '┼'
		} else {
			rows[y][x] = r
		}
	}
	for _, edge := range v.plan.Edges {
		from, to := positions[edge.From], positions[edge.To]
		bus := from.y + 2
		for y := from.y + 1; y <= bus; y++ {
			put(from.x, y, '│')
		}
		for x := min(from.x, to.x); x <= max(from.x, to.x); x++ {
			put(x, bus, '─')
		}
		for y := bus; y < to.y; y++ {
			put(to.x, y, '│')
		}
		if to.y > 0 {
			rows[to.y-1][to.x] = '▼'
		}
	}
	// Styled labels are applied after edges so connectors cannot erase names.
	labels := map[int]map[int]string{}
	for _, node := range v.plan.Nodes {
		pos, label := positions[node.ID], v.graphNode(node.ID)
		start := max(0, pos.x-ansi.StringWidth(label)/2)
		if labels[pos.y] == nil {
			labels[pos.y] = map[int]string{}
		}
		labels[pos.y][start] = label
	}
	lines := make([]string, len(rows))
	for y, row := range rows {
		var line strings.Builder
		for x := 0; x < width; {
			if label, ok := labels[y][x]; ok {
				line.WriteString(label)
				x += ansi.StringWidth(label)
			} else {
				line.WriteRune(row[x])
				x++
			}
		}
		lines[y] = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Separator).Render(strings.TrimRight(line.String(), " "))
	}
	var conditions []string
	for _, edge := range v.plan.Edges {
		if edge.Label != "" {
			conditions = append(conditions, edge.From+" → "+edge.To+" ["+edge.Label+"]")
		}
	}
	if len(conditions) > 0 {
		lines = append(lines, strings.Join(conditions, " · "))
	}
	return v.graphWindow(lines, positions[v.nodeID()].y)
}

func (v *WorkflowView) renderDependencyList(levels map[string]int) string {
	var lines []string
	for _, node := range v.plan.Nodes {
		var upstream []string
		for _, edge := range node.Upstream() {
			upstream = append(upstream, edge.From)
		}
		suffix := ""
		if len(upstream) > 0 {
			suffix = " ← " + strings.Join(upstream, ", ")
		}
		lines = append(lines, strings.Repeat("  ", min(levels[node.ID], 4))+v.graphNode(node.ID)+suffix)
	}
	return v.graphWindow(lines, v.selected)
}

func (v *WorkflowView) graphWindow(lines []string, selected int) string {
	budget := max(3, v.height-15)
	if len(lines) > budget {
		start := max(0, min(selected-budget/2, len(lines)-budget))
		lines = lines[start : start+budget]
	}
	return strings.Join(lines, "\n")
}

func (v *WorkflowView) marker(id string) string {
	state := v.record.Nodes[id].Status
	if state == workflow.StatusFailed {
		return lipgloss.NewStyle().Foreground(kit.CurrentTheme.Error).Render("●")
	}
	if v.record.Status == workflow.RunRunning && state == workflow.StatusRunning {
		colors := []kit.AdaptiveColor{kit.CurrentTheme.Muted, kit.CurrentTheme.Accent, kit.CurrentTheme.Primary, kit.CurrentTheme.TextBright, kit.CurrentTheme.Primary, kit.CurrentTheme.Accent}
		return lipgloss.NewStyle().Foreground(colors[v.phase%len(colors)]).Render("●")
	}
	if state == workflow.StatusSucceeded || state == workflow.StatusRunning {
		return "●"
	}
	return "○"
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

func (v *WorkflowView) renderDetail() string {
	id := v.nodeID()
	if id == "" {
		return ""
	}
	node := v.record.Nodes[id]
	elapsed := node.Duration
	if node.Status == workflow.StatusRunning && v.record.Status == workflow.RunRunning && !node.StartedAt.IsZero() {
		elapsed = time.Since(node.StartedAt)
	}
	title := id
	switch node.Status {
	case workflow.StatusFailed:
		title += " · Failed"
	case workflow.StatusSkipped:
		title += " · Blocked"
	case workflow.StatusOmitted:
		title += " · Not selected"
	}
	detail := kit.SelectorSelectedLabelStyle().Render(title)
	if (!node.StartedAt.IsZero() || node.Duration > 0) && !(node.Status == workflow.StatusRunning && v.record.Status != workflow.RunRunning) {
		duration := elapsed.Round(time.Second).String()
		detail += strings.Repeat(" ", max(1, v.width-4-ansi.StringWidth(detail)-len(duration))) + lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted).Render(duration)
	}
	if v.record.Status != workflow.RunRunning {
		return detail + "\n\nResult\n" + node.Output
	}
	var activity []string
	for _, entry := range v.record.Activity[id] {
		activity = append(activity, strings.Split(entry, "\n")...)
	}
	for padding := len(activity); padding < 3; padding++ {
		detail += "\n"
	}
	for i := max(0, len(activity)-3); i < len(activity); i++ {
		line := activity[i]
		if i < len(activity)-1 {
			line = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted).Render(line)
		}
		detail += "\n" + line
	}
	if len(node.Workers) > 0 {
		detail += fmt.Sprintf("\n%d workers · Enter for output and full history", len(node.Workers))
	}
	return detail
}
