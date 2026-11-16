package input

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/flow"
	"github.com/genai-io/san/internal/tool/perm"
)

func (v *FlowView) Render() string {
	if !v.active {
		return ""
	}
	title, content, hint := v.renderPage()
	switch v.confirm {
	case "save":
		title = "Save · " + v.name
		content = v.body.View() + "\nSave this flow in the project? y / n"
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
		if v.page == "inputs" && v.field < len(v.keys) {
			// Keep validation feedback from pushing the focused editor offscreen.
			content += "\n" + kit.DimStyle().Render(ansi.Truncate(strings.ReplaceAll(v.message, "\n", " "), max(1, v.width-2), "…"))
		} else {
			content = v.message + "\n\n" + content
		}
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

func (v *FlowView) renderPage() (title, content, hint string) {
	title, hint = "Flow", "↑/↓ select · Enter open · Esc close"
	switch v.page {
	case "generate":
		title, hint = "Create Flow", "Esc cancel"
		content = "Goal\n" + ansi.Wrap(v.goal, max(1, v.width-4), " ") + "\n\nDrafting a blueprint for review. Nothing will run automatically."
		if !v.drafting {
			content = "Goal\n" + ansi.Wrap(v.goal, max(1, v.width-4), " ") + "\n\nNo draft was created."
			hint = "Enter retry · Esc close"
		}
	case "hub":
		title, content = "Flow · definitions and recent runs", v.renderHub()
	case "review":
		title = "Review · " + v.name
		hint = "e edit · s save · Esc close"
		if v.saved {
			hint = "r run · " + hint
		} else {
			title += " · Unsaved"
		}
		if ansi.StringWidth("↑↓ · "+hint) <= v.width {
			hint = "↑↓ · " + hint
		}
		for _, extra := range []string{"d limits", "PgUp/Dn scroll"} {
			if ansi.StringWidth(hint+" · "+extra) <= v.width {
				hint += " · " + extra
			}
		}
		content = v.body.View()
		if v.plan != nil {
			content = v.renderGraph() + "\n\n" + content
		}
	case "details":
		title, content = "Execution limits · "+v.name, v.body.View()
		hint = "↑/↓ scroll · Esc review"
	case "edit":
		title = "Edit source · " + v.name
		content = v.templateHelp() + "\n\n" + v.editor.View()
		hint = "Ctrl+S preview save · Esc review"
	case "inputs":
		return v.renderInputs()
	case "run", "executions":
		return v.renderRun()
	}
	return title, content, hint
}

func flowInputLabel(key string) string {
	if key == "input" {
		return "Request"
	}
	words := strings.Fields(strings.NewReplacer("_", " ", "-", " ").Replace(key))
	if len(words) == 0 {
		return "Request"
	}
	label := strings.Join(words, " ")
	runes := []rune(label)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func (v *FlowView) renderInputs() (title, content, hint string) {
	title = "Run · " + v.name
	if v.field >= len(v.keys) {
		hint = "Enter run · Esc review"
		content = v.body.View()
	} else {
		hint = "Enter next · Esc review"
		key := v.keys[v.field]
		label := kit.SelectorSelectedLabelStyle().Render("● " + flowInputLabel(key))
		progress := kit.DimStyle().Render(fmt.Sprintf("Input %d of %d", v.field+1, len(v.keys)))
		budget := max(1, v.height-6)
		if v.message != "" {
			budget = max(1, budget-1)
		}
		editor := lipgloss.NewStyle().Background(kit.CurrentTheme.CommandBg).
			Padding(0, 1).MarginLeft(2).Render(v.editor.View())
		header := label + "\n" + progress
		if budget < lipgloss.Height(editor)+3 {
			header = label
		}
		if users := v.referenceUsers("input." + key); len(users) > 0 && budget >= lipgloss.Height(editor)+4 {
			header += "\n" + kit.DimStyle().Render(ansi.Truncate("Used by "+strings.Join(users, ", "), max(1, v.width-2), "…"))
		}
		if budget <= 1 {
			content = editor
		} else {
			content = header + "\n" + editor
		}
	}
	for _, extra := range []string{"Shift+Tab back", "Shift+Enter newline"} {
		if extra == "Shift+Enter newline" && v.field >= len(v.keys) {
			continue
		}
		if ansi.StringWidth(hint+" · "+extra) <= v.width {
			hint += " · " + extra
		}
	}
	return title, content, hint
}

// renderInputSummary supplies the scrollable confirmation body, not the run graph.
func (v *FlowView) renderInputSummary() string {
	lines := []string{kit.SelectorSelectedLabelStyle().Render("● Ready to run")}
	if v.plan != nil {
		lines = append(lines, kit.DimStyle().Render(kit.Plural(len(v.plan.Nodes), "step")))
	}
	if len(v.keys) == 0 {
		lines = append(lines, "", "No inputs needed.")
	}
	for _, key := range v.keys {
		lines = append(lines, "", kit.SelectorSelectedLabelStyle().Render(flowInputLabel(key)))
		value := ansi.Wrap(v.inputs[key], max(1, v.width-6), " ")
		lines = append(lines, "  "+strings.ReplaceAll(value, "\n", "\n  "))
	}
	return strings.Join(lines, "\n")
}

func (v *FlowView) renderHub() string {
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

func (v *FlowView) renderRun() (title, content, hint string) {
	title, hint = "Flow", "↑/↓ pin · f follow · Enter inspect · Esc close"
	if v.record == nil {
		return title, "Run unavailable", hint
	}
	elapsed := time.Since(v.record.StartedAt)
	if !v.record.FinishedAt.IsZero() {
		elapsed = v.record.FinishedAt.Sub(v.record.StartedAt)
	} else if v.record.Status != flow.RunRunning {
		elapsed = max(0, v.record.UpdatedAt.Sub(v.record.StartedAt))
	}
	left, right := "Run · "+v.record.Name, elapsed.Round(time.Second).String()
	title = left + strings.Repeat(" ", max(1, v.width-4-lipgloss.Width(left)-len(right))) + right
	if v.page == "executions" {
		content = "Executions · " + v.nodeID() + "\n" + v.renderExecutions()
		hint = "↑/↓ select · Enter native task · Esc graph"
	} else {
		content = v.renderGraph() + "\n\n" + v.renderDetail()
		switch v.record.Status {
		case flow.RunRunning:
			hint = "s stop · " + hint
		case flow.RunSucceeded:
			hint = "R rerun selected · " + hint
		default:
			hint = "r resume · R rerun · " + hint
		}
	}
	if v.record.Status == flow.RunFailed || v.record.Status == flow.RunStopped || v.record.Status == flow.RunInterrupted {
		content = "Run " + strings.ToUpper(v.record.Status) + "\n" + content
	}
	if v.record.Error != "" {
		content = "Error: " + v.record.Error + "\n" + content
	}
	return title, content, hint
}

func (v *FlowView) renderReview() string {
	if v.plan == nil {
		return "Press e to fix the source before reviewing its inputs and outputs."
	}
	node, ok := v.plan.Node(v.nodeID())
	if !ok {
		return "Select a step to review its inputs and output."
	}
	lines := []string{fmt.Sprintf("Step %d/%d · %s", v.selected+1, len(v.plan.Nodes), node.ID), ""}
	agent := node.Config["agent"]
	if agent == "" {
		agent = "default"
	}
	for _, key := range []string{"mode", "model"} {
		if value := node.Config[key]; value != "" {
			agent += " · " + value
		}
	}
	lines = append(lines, "  Agent   "+agent)
	if node.ForEach != "" {
		lines = append(lines, "  Items   JSON array from "+node.ForEach)
	}
	if refs := node.References(); len(refs) == 0 {
		lines = append(lines, "  Input   No inserted values; uses the instructions below.")
	} else {
		for _, ref := range refs {
			origin := "final answer from step " + ref
			if key, ok := v.plan.InputKey(ref); ok {
				origin = key + " you enter before running"
				if ref == "input" {
					origin = "what you enter before running"
				}
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
		lines = append(lines, "          Final flow result")
	}
	lines = append(lines, "", "Instructions", "  "+strings.ReplaceAll(node.Prompt, "\n", "\n  "))
	return ansi.Wrap(strings.Join(lines, "\n"), max(1, v.width-4), " ")
}

func (v *FlowView) referenceUsers(ref string) []string {
	var users []string
	seen := map[string]bool{}
	if v.plan != nil {
		key, isInput := v.plan.InputKey(ref)
		for _, node := range v.plan.Nodes {
			refs := node.References()
			if source, _, _ := strings.Cut(node.ForEach, "."); source != "" {
				refs = append(refs, source)
			}
			for _, name := range refs {
				otherKey, otherInput := v.plan.InputKey(name)
				if (name == ref || isInput && otherInput && key == otherKey) && !seen[node.Base] {
					users = append(users, node.Base)
					seen[node.Base] = true
				}
			}
		}
	}
	return users
}

func (v *FlowView) templateHelp() string {
	if v.plan == nil {
		return "{{input}} = what you enter before running\n{{step}} = the final answer from that upstream step"
	}
	var inputs, outputs []string
	seen := map[string]bool{}
	for _, node := range v.plan.Nodes {
		for _, ref := range node.References() {
			if _, ok := v.plan.InputKey(ref); ok && !seen[ref] {
				inputs = append(inputs, "{{"+ref+"}}")
				seen[ref] = true
			}
		}
	}
	seen = map[string]bool{}
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

func (v *FlowView) renderExecutions() string {
	var lines []string
	for i, c := range v.executionChoices() {
		prefix := "  "
		if i == v.cursor {
			prefix = "› "
		}
		lines = append(lines, prefix+c.label)
	}
	return strings.Join(lines, "\n")
}

func (v *FlowView) renderLimits() string {
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

func (v *FlowView) diffPreview() string {
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

func (v *FlowView) resumePreview() string {
	if v.record == nil || v.plan == nil {
		return "Run definition unavailable; recovery cannot be previewed"
	}
	states, err := flow.Retry(v.plan, v.record.Nodes)
	if v.resumeFrom != "" {
		states, err = flow.Invalidate(v.plan, v.record.Nodes, v.resumeFrom)
	}
	if err != nil {
		return err.Error()
	}
	lines := []string{"Resume preview — no automatic retry.", "Unknown/in-flight work may already have side effects; retry can duplicate them."}
	for _, node := range v.plan.Nodes {
		state := states[node.ID]
		action := "rerun"
		if state.Status == flow.StatusSucceeded {
			action = "reuse successful result"
		} else if state.Status == flow.StatusOmitted || state.Status == flow.StatusSkipped {
			action = "re-evaluate dependencies and branch"
		}
		finished := 0
		for _, worker := range state.Workers {
			if worker.State.Status == flow.StatusSucceeded {
				finished++
			}
		}
		if finished > 0 && state.Status != flow.StatusSucceeded {
			action += fmt.Sprintf("; reuse %d successful workers if plan unchanged", finished)
		}
		lines = append(lines, node.ID+": "+action)
	}
	return strings.Join(lines, "\n")
}

// Coordinates depend only on topology; completion and selection never reorder nodes.
func (v *FlowView) renderGraph() string {
	if v.plan == nil {
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

func (v *FlowView) renderDAG(levels map[string]int, depth int) string {
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

func (v *FlowView) renderDependencyList(levels map[string]int) string {
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

func (v *FlowView) graphWindow(lines []string, selected int) string {
	budget := max(3, v.height-15)
	if len(lines) > budget {
		start := max(0, min(selected-budget/2, len(lines)-budget))
		lines = lines[start : start+budget]
	}
	return strings.Join(lines, "\n")
}

func (v *FlowView) marker(id string) string {
	if v.record == nil {
		return "○"
	}
	state := v.record.Nodes[id].Status
	if state == flow.StatusFailed {
		return lipgloss.NewStyle().Foreground(kit.CurrentTheme.Error).Render("●")
	}
	if v.record.Status == flow.RunRunning && state == flow.StatusRunning {
		colors := []kit.AdaptiveColor{kit.CurrentTheme.Muted, kit.CurrentTheme.Accent, kit.CurrentTheme.Primary, kit.CurrentTheme.TextBright, kit.CurrentTheme.Primary, kit.CurrentTheme.Accent}
		return lipgloss.NewStyle().Foreground(colors[v.phase%len(colors)]).Render("●")
	}
	if state == flow.StatusSucceeded || state == flow.StatusRunning {
		return "●"
	}
	return "○"
}

func (v *FlowView) graphNode(id string) string {
	style := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Text)
	state := flow.StatusPending
	if v.record != nil {
		state = v.record.Nodes[id].Status
	}
	switch state {
	case "", flow.StatusPending, flow.StatusSkipped, flow.StatusOmitted:
		style = style.Foreground(kit.CurrentTheme.TextDisabled)
	case flow.StatusSucceeded:
		style = style.Foreground(kit.CurrentTheme.TextDim)
	}
	if id == v.nodeID() {
		style = kit.SelectorSelectedLabelStyle()
	}
	return " " + v.marker(id) + " " + style.Render(id) + " "
}

func (v *FlowView) renderDetail() string {
	id := v.nodeID()
	if id == "" {
		return ""
	}
	node := v.record.Nodes[id]
	elapsed := node.Duration
	if node.Status == flow.StatusRunning && v.record.Status == flow.RunRunning && !node.StartedAt.IsZero() {
		elapsed = time.Since(node.StartedAt)
	}
	title := id
	switch node.Status {
	case flow.StatusFailed:
		title += " · Failed"
	case flow.StatusSkipped:
		title += " · Blocked"
	case flow.StatusOmitted:
		title += " · Not selected"
	}
	detail := kit.SelectorSelectedLabelStyle().Render(title)
	if (!node.StartedAt.IsZero() || node.Duration > 0) && !(node.Status == flow.StatusRunning && v.record.Status != flow.RunRunning) {
		duration := elapsed.Round(time.Second).String()
		detail += strings.Repeat(" ", max(1, v.width-4-ansi.StringWidth(detail)-len(duration))) + lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted).Render(duration)
	}
	return detail + fmt.Sprintf("\nStatus: %s · Enter to inspect native executions", node.Status)
}
