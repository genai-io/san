package workflow

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

const (
	graphWidth = 72
	boxGap     = 6
	rowStep    = 4
)

// ProgressView draws a left-to-right graph with each node's current phase.
// Wider or denser graphs use a dependency list to stay legible in a terminal.
func (w *Workflow) ProgressView(statuses map[string]Status) string {
	// A nil status map is a structural preview. Once a run starts, callers
	// pass a (possibly empty) map so pending and the other phases are visible.
	preview := statuses == nil
	finished := 0
	for _, n := range w.Nodes {
		switch statuses[n.ID] {
		case StatusSucceeded, StatusFailed, StatusSkipped, StatusOmitted:
			finished++
		}
	}
	name := w.Name
	if name == "" {
		name = "(unnamed)"
	}
	header := fmt.Sprintf("Workflow %s (%d/%d finished)", name, finished, len(w.Nodes))
	if preview {
		unit := "steps"
		if len(w.Nodes) == 1 {
			unit = "step"
		}
		header = fmt.Sprintf("Workflow %s (%d %s)", name, len(w.Nodes), unit)
	}
	if len(w.Loops) == 1 {
		if preview {
			header = fmt.Sprintf("Workflow %s (up to %d turns)", name, len(w.Nodes))
		} else {
			header = fmt.Sprintf("Workflow %s (%d/%d turns finished)", name, finished, len(w.Nodes))
		}
		if graph, ok := w.loopBoxGraph(statuses); ok {
			return header + "\n" + graph + viewLegend(preview)
		}
	}
	if graph, ok := w.boxGraph(statuses); ok {
		return header + "\n" + graph + viewLegend(preview)
	}
	return header + "\n" + w.dependencyList(statuses) + viewLegend(preview)
}

const graphLegend = "\n  ○ pending  ● running  ✓ done  ✗ failed\n  ↷ blocked  ⊘ not selected"

func viewLegend(preview bool) string {
	if preview {
		return ""
	}
	return graphLegend
}

// CompactProgressView shows one small graph. A branch and join uses a few
// aligned rows; other complete stages use one line. Complex dependencies keep
// their exact edge list instead of being squeezed into a misleading picture.
func (w *Workflow) CompactProgressView(statuses map[string]Status, width int) string {
	if width <= 0 {
		width = 108
	}
	finished := 0
	for _, n := range w.Nodes {
		switch statuses[n.ID] {
		case StatusSucceeded, StatusFailed, StatusSkipped, StatusOmitted:
			finished++
		}
	}
	name := w.Name
	if name == "" {
		name = "(unnamed)"
	}
	header := fmt.Sprintf("Workflow %s · %d/%d finished", name, finished, len(w.Nodes))
	if statuses == nil {
		unit := "steps"
		if len(w.Nodes) == 1 {
			unit = "step"
		}
		header = fmt.Sprintf("Workflow %s · %d %s", name, len(w.Nodes), unit)
	}
	header = "  " + ansi.Truncate(header, max(1, width-2), "…")
	if len(w.Loops) == 0 {
		ordered := w.topologicalNodes()
		layer := make(map[string]int, len(ordered))
		var stages [][]*Node
		for _, n := range ordered {
			for _, e := range n.upstream {
				layer[n.ID] = max(layer[n.ID], layer[e.From]+1)
			}
			for len(stages) <= layer[n.ID] {
				stages = append(stages, nil)
			}
			stages[layer[n.ID]] = append(stages[layer[n.ID]], n)
		}
		expected := 0
		for i := 1; i < len(stages); i++ {
			expected += len(stages[i-1]) * len(stages[i])
		}
		if len(ordered) > 0 && len(w.Edges) == expected {
			valid := true
			seen := make(map[string]bool, len(w.Edges))
			for _, e := range w.Edges {
				key := e.From + "\x00" + e.To
				if e.Label != "" || layer[e.To] != layer[e.From]+1 || seen[key] {
					valid = false
					break
				}
				seen[key] = true
			}
			if valid {
				if graph, ok := branchJoinGraph(stages, statuses, width); ok {
					return header + "\n" + graph
				}
				var parts []string
				for _, stage := range stages {
					var labels []string
					for _, n := range stage {
						label := n.ID
						if statuses != nil {
							label += " " + statusGlyph(statuses[n.ID])
						}
						labels = append(labels, label)
					}
					if len(labels) > 1 {
						parts = append(parts, "["+strings.Join(labels, " ∥ ")+"]")
					} else {
						parts = append(parts, labels[0])
					}
				}
				line := "    " + strings.Join(parts, " → ")
				if ansi.StringWidth(line) < width {
					return header + "\n" + line
				}
			}
		}
	}
	var lines []string
	for line := range strings.SplitSeq(w.dependencyList(statuses), "\n") {
		lines = append(lines, "    "+wrapActivity(strings.TrimPrefix(line, "  "), max(1, width-4), "      "))
	}
	return header + "\n" + strings.Join(lines, "\n")
}

func branchJoinGraph(stages [][]*Node, statuses map[string]Status, width int) (string, bool) {
	if len(stages) != 3 || len(stages[0]) != 1 || len(stages[1]) < 2 || len(stages[2]) != 1 {
		return "", false
	}
	label := func(n *Node) string {
		if statuses == nil {
			return n.ID
		}
		return n.ID + " " + statusGlyph(statuses[n.ID])
	}
	left := "    " + label(stages[0][0]) + " ─"
	indent := strings.Repeat(" ", ansi.StringWidth(left))
	join := label(stages[2][0])
	branchWidth := 0
	for _, n := range stages[1] {
		branchWidth = max(branchWidth, ansi.StringWidth(label(n)))
	}
	var lines []string
	for i, n := range stages[1] {
		branch := label(n)
		branch += strings.Repeat(" ", branchWidth-ansi.StringWidth(branch))
		start := indent
		connector := "├"
		if i == 0 {
			start, connector = left, "┬"
		} else if i == len(stages[1])-1 {
			connector = "└"
		}
		end := "┼"
		if i == 0 {
			end = "┐"
		} else if i == len(stages[1])-1 {
			end = "┘"
		}
		if i == len(stages[1])/2 && i == len(stages[1])-1 {
			end = "┴"
		}
		line := start + connector + "──▶ " + branch + " ─" + end
		if i == len(stages[1])/2 {
			line += "──▶ " + join
		}
		if ansi.StringWidth(line) >= width {
			return "", false
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), true
}

// ActivityEvent is one chronological status or subagent update in a live run.
type ActivityEvent struct {
	Node, Worker, Text string
	ToolID             string
	ToolResult         string
	ToolFinished       bool
	ToolFailed         bool
}

// ActivityWindow is the number of recent call, text, and status entries kept
// in the compact live view. A completed call may occupy two display lines.
const ActivityWindow = 8

// ActivityStreamView groups the latest events by node or for_each worker so
// parallel agents have visible boundaries. Long calls wrap inside each block.
func (w *Workflow) ActivityStreamView(events []ActivityEvent, width int) string {
	if width <= 0 {
		width = 108
	}
	width = max(8, width-2)
	if len(events) > ActivityWindow {
		events = events[len(events)-ActivityWindow:]
	}
	var b strings.Builder
	b.WriteString("  Activity")
	if len(events) == 0 {
		b.WriteString("\n    Waiting for the first node")
		return b.String()
	}
	type group struct {
		label  string
		events []ActivityEvent
	}
	var groups []group
	byLabel := make(map[string]int)
	for _, event := range events {
		label := event.Node
		if event.Worker != "" {
			label += "·" + event.Worker
		}
		index, exists := byLabel[label]
		if !exists {
			index = len(groups)
			byLabel[label] = index
			groups = append(groups, group{label: label})
		}
		groups[index].events = append(groups[index].events, event)
	}
	for _, group := range groups {
		label := ansi.Truncate(group.label, max(1, width-8), "…")
		b.WriteByte('\n')
		b.WriteString("    ╭─ " + label)
		for _, event := range group.events {
			message := event.Text
			indent := "    │  "
			if event.ToolID != "" {
				marker := "●"
				if event.ToolFinished {
					marker = "✓"
					if event.ToolFailed {
						marker = "✗"
					}
				}
				message = marker + " " + message
				indent = "    │    "
			}
			b.WriteByte('\n')
			b.WriteString("    │  ")
			b.WriteString(wrapActivity(message, max(1, width-ansi.StringWidth(indent)), indent))
			if event.ToolID != "" && event.ToolFinished && event.ToolResult != "" {
				const resultPrefix = "    │    └ "
				const continuation = "    │      "
				b.WriteByte('\n')
				b.WriteString(resultPrefix)
				b.WriteString(wrapActivity(event.ToolResult, max(1, width-ansi.StringWidth(resultPrefix)), continuation))
			}
		}
		b.WriteString("\n    ╰─")
	}
	return b.String()
}

func wrapActivity(s string, width int, indent string) string {
	var b strings.Builder
	lineWidth := 0
	newLine := func() {
		b.WriteByte('\n')
		b.WriteString(indent)
		lineWidth = 0
	}
	for _, word := range strings.Fields(s) {
		wordWidth := ansi.StringWidth(word)
		if lineWidth > 0 && lineWidth+1+wordWidth > width {
			newLine()
		} else if lineWidth > 0 {
			b.WriteByte(' ')
			lineWidth++
		}
		for _, r := range word {
			charWidth := ansi.StringWidth(string(r))
			if lineWidth+charWidth > width {
				newLine()
			}
			b.WriteRune(r)
			lineWidth += charWidth
		}
	}
	return b.String()
}

// PreviewSteps pairs graph labels with prompt summaries that fit the current
// display width. Retry rounds share one authored node and appear only once.
func (w *Workflow) PreviewSteps(width int) string {
	if width <= 0 {
		width = 108
	}
	seen := make(map[string]bool)
	var lines []string
	for _, n := range w.topologicalNodes() {
		if seen[n.Base] {
			continue
		}
		seen[n.Base] = true
		first, _, _ := strings.Cut(strings.TrimSpace(n.Prompt), "\n")
		first = strings.TrimSpace(first)
		if first == "" {
			continue
		}
		prefix := "  " + n.Base + " — "
		first = ansi.Truncate(first, max(8, width-ansi.StringWidth(prefix)), "…")
		lines = append(lines, prefix+first)
		if len(lines) == 8 {
			break
		}
	}
	return strings.Join(lines, "\n")
}

// loopBoxGraph folds the unrolled rounds back into one box per authored node.
// The runner still uses the expanded graph; this projection is display-only.
func (w *Workflow) loopBoxGraph(statuses map[string]Status) (string, bool) {
	loop := w.Loops[0]
	compact := &Workflow{}
	byBase := make(map[string]*Node, len(w.Nodes))
	baseStatus := make(map[string]Status, len(w.Nodes))
	currentRound := 0
	for _, n := range w.Nodes {
		if _, exists := byBase[n.Base]; !exists {
			clone := *n
			clone.ID = n.Base
			clone.upstream = nil
			clone.downstream = nil
			byBase[n.Base] = &clone
			compact.Nodes = append(compact.Nodes, &clone)
		}
		s := statuses[n.ID]
		switch s {
		case StatusRunning, StatusSucceeded, StatusFailed:
			baseStatus[n.Base] = s // later rounds appear later in w.Nodes
			if w.loopOf[n.Base] != nil {
				if suffix, ok := strings.CutPrefix(n.ID, n.Base+"#"); ok {
					if round, err := strconv.Atoi(suffix); err == nil {
						currentRound = max(currentRound, round)
					}
				}
			}
		case StatusSkipped, StatusOmitted:
			if baseStatus[n.Base] == "" {
				baseStatus[n.Base] = s
			}
		}
	}
	seen := map[Edge]bool{}
	for _, e := range w.Edges {
		from, to := w.byID[e.From].Base, w.byID[e.To].Base
		if from == to || (from == loop.From && to == loop.To && e.Label == loop.Label) {
			continue // the back edge is drawn by the loop annotation below
		}
		baseEdge := Edge{From: from, To: to, Label: e.Label}
		if seen[baseEdge] {
			continue
		}
		seen[baseEdge] = true
		compact.Edges = append(compact.Edges, baseEdge)
		byBase[from].downstream = append(byBase[from].downstream, baseEdge)
		byBase[to].upstream = append(byBase[to].upstream, baseEdge)
	}
	if statuses == nil {
		baseStatus = nil
	}
	graph, positions, ok := compact.boxGraphLayout(baseStatus)
	if !ok {
		return "", false
	}
	if arched, ok := loopArc(graph, positions[loop.To], positions[loop.From], loop); ok {
		graph = arched
	}
	annotation := fmt.Sprintf("  ↶ %s ─%s x%d→ %s", loop.From, loop.Label, loop.Rounds, loop.To)
	if currentRound > 0 {
		annotation += fmt.Sprintf(" (round %d/%d)", currentRound, loop.Rounds)
	}
	return graph + "\n" + annotation, true
}

// loopArc connects the retry source back to its head above a one-row graph.
// Other shapes keep the explicit loop annotation below the boxes.
func loopArc(graph string, head, tail boxPosition, loop Loop) (string, bool) {
	if head.y != 0 || tail.y != 0 || head.x >= tail.x {
		return "", false
	}
	left, right := head.x+head.width/2, tail.x+tail.width/2
	label := []rune(" " + loop.Label + " x" + strconv.Itoa(loop.Rounds) + " ")
	if right-left < len(label)+2 {
		return "", false
	}
	arch := []rune(strings.Repeat(" ", right+1))
	for x := left + 1; x < right; x++ {
		arch[x] = '─'
	}
	arch[left], arch[right] = '╭', '╮'
	labelX := left + (right-left-len(label))/2
	for i, ch := range label {
		arch[labelX+i] = ch
	}
	stem := []rune(strings.Repeat(" ", right+1))
	stem[left], stem[right] = '│', '│'
	arrow := []rune(strings.Repeat(" ", right+1))
	arrow[left], arrow[right] = '▼', '│'
	rows := strings.Split(graph, "\n")
	top := []rune(rows[0])
	top[left], top[right] = '┴', '┴'
	rows[0] = string(top)
	return strings.TrimRight(string(arch), " ") + "\n" +
		strings.TrimRight(string(stem), " ") + "\n" +
		strings.TrimRight(string(arrow), " ") + "\n" +
		strings.Join(rows, "\n"), true
}

type boxPosition struct {
	x, y, width int
}

// boxGraph lays out columns by longest path and centers shorter columns.
// Every drawn edge stays in the gutter between adjacent columns.
func (w *Workflow) boxGraph(statuses map[string]Status) (string, bool) {
	graph, _, ok := w.boxGraphLayout(statuses)
	return graph, ok
}

func (w *Workflow) boxGraphLayout(statuses map[string]Status) (string, map[string]boxPosition, bool) {
	ordered := w.topologicalNodes()
	if len(ordered) == 0 {
		return "", nil, false
	}
	layer := make(map[string]int, len(ordered))
	var columns [][]*Node
	for _, n := range ordered {
		for _, e := range n.upstream {
			layer[n.ID] = max(layer[n.ID], layer[e.From]+1)
		}
		for len(columns) <= layer[n.ID] {
			columns = append(columns, nil)
		}
		columns[layer[n.ID]] = append(columns[layer[n.ID]], n)
	}
	// A long edge would pass through another column's boxes. Keep the full
	// dependency list instead of showing a broken or misleading connection.
	for _, e := range w.Edges {
		if layer[e.To] != layer[e.From]+1 {
			return "", nil, false
		}
	}

	widths := make([]int, len(columns))
	maxRows := 0
	hasDetails := false
	for i, nodes := range columns {
		maxRows = max(maxRows, len(nodes))
		widths[i] = 12
		for _, n := range nodes {
			widths[i] = max(widths[i], len(n.ID)+6)
			if detail := nodeDetail(n); detail != "" {
				hasDetails = true
				widths[i] = max(widths[i], len(detail)+4)
			}
		}
	}
	if maxRows > 3 {
		return "", nil, false
	}
	xs := make([]int, len(columns))
	xs[0] = 2
	for i := 1; i < len(columns); i++ {
		xs[i] = xs[i-1] + widths[i-1] + boxGap
	}
	width := xs[len(xs)-1] + widths[len(widths)-1]
	if width > graphWidth {
		return "", nil, false
	}
	step, boxHeight := rowStep, 3
	if hasDetails {
		step, boxHeight = rowStep+1, 4
	}
	height := (maxRows-1)*step + boxHeight
	canvas := make([][]rune, height)
	lines := make([][]uint8, height)
	for y := range canvas {
		canvas[y] = []rune(strings.Repeat(" ", width))
		lines[y] = make([]uint8, width)
	}
	positions := make(map[string]boxPosition, len(ordered))
	for col, nodes := range columns {
		startY := (maxRows - len(nodes)) * step / 2
		for i, n := range nodes {
			positions[n.ID] = boxPosition{x: xs[col], y: startY + i*step, width: widths[col]}
		}
	}
	for _, e := range w.Edges {
		from, to := positions[e.From], positions[e.To]
		startX, endX := from.x+from.width, to.x-2
		startY, endY := from.y+1, to.y+1
		if startY == endY {
			drawHorizontal(lines, startX, endX, startY)
		} else {
			middle := (startX + endX) / 2
			drawHorizontal(lines, startX, middle, startY)
			drawVertical(lines, middle, startY, endY)
			drawHorizontal(lines, middle, endX, endY)
		}
		canvas[endY][to.x-1] = '▶'
	}
	for y, row := range lines {
		for x, bits := range row {
			if bits != 0 {
				canvas[y][x] = lineRune(bits)
			}
		}
	}
	for _, n := range ordered {
		p := positions[n.ID]
		put(canvas[p.y], p.x, "╭"+strings.Repeat("─", p.width-2)+"╮")
		if statuses == nil {
			put(canvas[p.y+1], p.x, "│ "+fmt.Sprintf("%-*s", p.width-4, n.ID)+" │")
		} else {
			status := statuses[n.ID]
			if status == "" {
				status = StatusPending
			}
			put(canvas[p.y+1], p.x, "│ "+fmt.Sprintf("%-*s", p.width-6, n.ID)+" "+statusGlyph(status)+" │")
		}
		if hasDetails {
			put(canvas[p.y+2], p.x, "│ "+fmt.Sprintf("%-*s", p.width-4, nodeDetail(n))+" │")
		}
		put(canvas[p.y+boxHeight-1], p.x, "╰"+strings.Repeat("─", p.width-2)+"╯")
	}
	out := make([]string, 0, height)
	for _, row := range canvas {
		out = append(out, strings.TrimRight(string(row), " "))
	}
	graph := strings.Join(out, "\n")
	// Conditional labels remain visible even where several arrows join.
	for _, e := range w.Edges {
		if e.Label != "" {
			graph += fmt.Sprintf("\n  %s ─%s→ %s", e.From, e.Label, e.To)
		}
	}
	return graph, positions, true
}

func nodeDetail(n *Node) string {
	for _, key := range []string{"model", "agent", "mode"} {
		if value := n.Config[key]; value != "" {
			return value
		}
	}
	return ""
}

const (
	lineNorth uint8 = 1 << iota
	lineEast
	lineSouth
	lineWest
)

func drawHorizontal(lines [][]uint8, from, to, y int) {
	for x := from; x < to; x++ {
		lines[y][x] |= lineEast
		lines[y][x+1] |= lineWest
	}
}

func drawVertical(lines [][]uint8, x, from, to int) {
	if from > to {
		from, to = to, from
	}
	for y := from; y < to; y++ {
		lines[y][x] |= lineSouth
		lines[y+1][x] |= lineNorth
	}
}

func lineRune(bits uint8) rune {
	switch bits {
	case lineNorth | lineSouth:
		return '│'
	case lineEast | lineSouth:
		return '┌'
	case lineSouth | lineWest:
		return '┐'
	case lineNorth | lineEast:
		return '└'
	case lineNorth | lineWest:
		return '┘'
	case lineNorth | lineEast | lineSouth:
		return '├'
	case lineNorth | lineSouth | lineWest:
		return '┤'
	case lineEast | lineSouth | lineWest:
		return '┬'
	case lineNorth | lineEast | lineWest:
		return '┴'
	case lineNorth | lineEast | lineSouth | lineWest:
		return '┼'
	case lineNorth, lineSouth:
		return '│'
	default:
		return '─'
	}
}

func put(row []rune, x int, text string) {
	for _, ch := range text {
		row[x] = ch
		x++
	}
}

func (w *Workflow) dependencyList(statuses map[string]Status) string {
	var b strings.Builder
	for _, n := range w.topologicalNodes() {
		if statuses == nil {
			fmt.Fprintf(&b, "  %s", n.ID)
		} else {
			status := statuses[n.ID]
			if status == "" {
				status = StatusPending
			}
			fmt.Fprintf(&b, "  %s %s", statusGlyph(status), n.ID)
		}
		if len(n.upstream) > 0 {
			b.WriteString("  ← ")
			for i, edge := range n.upstream {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(edge.From)
				if edge.Label != "" {
					fmt.Fprintf(&b, " [%s]", edge.Label)
				}
			}
		}
		b.WriteByte('\n')
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// topologicalNodes keeps the view in execution order even if markdown
// sections were written in another order.
func (w *Workflow) topologicalNodes() []*Node {
	ordered := make([]*Node, 0, len(w.Nodes))
	seen := make(map[string]bool, len(w.Nodes))
	for len(ordered) < len(w.Nodes) {
		advanced := false
		for _, n := range w.Nodes {
			if seen[n.ID] {
				continue
			}
			ready := true
			for _, edge := range n.upstream {
				if !seen[edge.From] {
					ready = false
					break
				}
			}
			if ready {
				ordered = append(ordered, n)
				seen[n.ID] = true
				advanced = true
			}
		}
		if !advanced {
			break // Parse rejects cycles; avoid a stuck renderer if handed one.
		}
	}
	return ordered
}
