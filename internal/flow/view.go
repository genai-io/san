package flow

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// Title returns a display label without changing the authored name:
// separators become spaces, a leading "flow" word is dropped, and the first
// letter is capitalized.
func (b *Blueprint) Title() string {
	words := strings.FieldsFunc(b.Name, func(r rune) bool {
		return r == '_' || r == '-' || unicode.IsSpace(r)
	})
	if len(words) > 1 && strings.EqualFold(words[0], "flow") {
		words = words[1:]
	}
	if len(words) == 0 {
		return "Untitled flow"
	}
	title := []rune(strings.Join(words, " "))
	title[0] = unicode.ToUpper(title[0])
	return string(title)
}

// CompactProgressView shows one small graph. A branch and join uses a few
// aligned rows; other complete stages use one line. Complex dependencies keep
// their exact edge list instead of being squeezed into a misleading picture.
func (b *Blueprint) CompactProgressView(statuses map[string]Status, width int) string {
	if width <= 0 {
		width = 108
	}
	finished := 0
	for _, n := range b.Nodes {
		switch statuses[n.ID] {
		case StatusSucceeded, StatusFailed, StatusSkipped, StatusOmitted:
			finished++
		}
	}
	progress := fmt.Sprintf("%d/%d", finished, len(b.Nodes))
	if statuses == nil {
		unit := "steps"
		if len(b.Nodes) == 1 {
			unit = "step"
		}
		progress = fmt.Sprintf("%d %s", len(b.Nodes), unit)
	}
	loopGraph, loopProgress := b.compactLoop(statuses, width)
	if loopGraph != "" {
		progress = loopProgress
	}
	// Align the title and progress with the graph's left edge.
	available := max(1, width-4)
	suffix := " · " + progress
	nameWidth := available - ansi.StringWidth(suffix)
	header := progress
	if nameWidth > 0 {
		header = ansi.Truncate("Flow · "+b.Title(), nameWidth, "…") + suffix
	}
	header = strings.Repeat(" ", min(4, max(0, width-1))) + ansi.Truncate(header, available, "…")
	if loopGraph != "" {
		return header + "\n" + loopGraph
	}
	if len(b.Loops) == 0 {
		ordered := b.topologicalNodes()
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
		if len(ordered) > 0 && len(b.Edges) == expected {
			valid := true
			seen := make(map[string]bool, len(b.Edges))
			for _, e := range b.Edges {
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
							label = progressGlyph(statuses[n.ID]) + " " + label
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
	for line := range strings.SplitSeq(b.dependencyList(statuses), "\n") {
		lines = append(lines, "    "+wrapLine(strings.TrimPrefix(line, "  "), max(1, width-4), "      "))
	}
	return header + "\n" + strings.Join(lines, "\n")
}

// compactLoop accepts only a single authored chain with one bounded return.
// Anything else keeps the exact dependency view.
func (b *Blueprint) compactLoop(statuses map[string]Status, width int) (string, string) {
	if len(b.Loops) != 1 {
		return "", ""
	}
	loop := b.Loops[0]
	bases := map[string]string{}
	instances := map[string][]*Node{}
	for _, n := range b.Nodes {
		base := n.Base
		if base == "" {
			base = n.ID
		}
		bases[n.ID] = base
		instances[base] = append(instances[base], n)
	}
	next := map[string]Edge{}
	incoming := map[string]bool{}
	for _, e := range b.Edges {
		from, to := bases[e.From], bases[e.To]
		if from == loop.From && to == loop.To && e.Label == loop.Label {
			continue // Synthetic bridge between retry instances.
		}
		edge := Edge{From: from, To: to, Label: e.Label}
		if prev, ok := next[from]; ok {
			if prev != edge {
				return "", ""
			}
			continue
		}
		if incoming[to] || (e.Label != "" && from != loop.From) {
			return "", ""
		}
		next[from], incoming[to] = edge, true
	}
	start := ""
	for base := range instances {
		if !incoming[base] {
			if start != "" {
				return "", ""
			}
			start = base
		}
	}
	var chain []string
	for id := start; id != "" && len(chain) <= len(instances); id = next[id].To {
		chain = append(chain, id)
	}
	if len(chain) != len(instances) {
		return "", ""
	}
	head, tail := -1, -1
	for i, id := range chain {
		if id == loop.To {
			head = i
		}
		if id == loop.From {
			tail = i
		}
	}
	if head < 0 || tail < head || tail+1 >= len(chain) || next[loop.From].Label == "" {
		return "", ""
	}
	round := 1
	for i := head; i <= tail; i++ {
		for _, n := range instances[chain[i]] {
			switch statuses[n.ID] {
			case StatusRunning, StatusSucceeded, StatusFailed:
				var r int
				if _, err := fmt.Sscanf(strings.TrimPrefix(n.ID, n.Base+"#"), "%d", &r); err == nil {
					round = max(round, r)
				}
			}
		}
	}
	done, total := 0, 0
	for i, id := range chain {
		rounds := 1
		if i >= head && i <= tail {
			rounds = round
		}
		for r := 1; r <= rounds; r++ {
			nodeID := instances[id][0].ID
			if i >= head && i <= tail {
				nodeID = fmt.Sprintf("%s#%d", id, r)
			}
			total++
			switch statuses[nodeID] {
			case StatusSucceeded, StatusFailed, StatusSkipped, StatusOmitted:
				done++
			}
		}
	}
	progress := fmt.Sprintf("round %d/%d · %d/%d", round, loop.Rounds, done, total)
	if statuses == nil {
		progress = fmt.Sprintf("up to %d rounds", loop.Rounds)
	}
	labels := make([]string, len(chain))
	running := make([]bool, len(chain))
	for i, id := range chain {
		labels[i] = id
		if statuses != nil {
			nodeID := instances[id][0].ID
			if i >= head && i <= tail {
				nodeID = fmt.Sprintf("%s#%d", id, round)
			}
			running[i] = statuses[nodeID] == StatusRunning
			labels[i] = progressGlyph(statuses[nodeID]) + " " + id
		}
		labels[i] = "[" + labels[i] + "]"
	}
	returnLabel := " " + loop.Label
	if round > 1 {
		returnLabel += fmt.Sprintf(" ×%d", round-1)
	}
	returnLabel += " "
	returnStroke := "─"
	if round > 1 && running[head] {
		returnStroke = "━"
	}
	line := "    "
	left, right := 0, 0
	for i, label := range labels {
		if i > 0 {
			stroke := "─"
			if running[i] && !(i == head && round > 1) {
				stroke = "━"
			}
			if i == tail {
				stroke = strings.Repeat(stroke, max(1, ansi.StringWidth(returnLabel)+2-(ansi.StringWidth(line)-left)-ansi.StringWidth(next[chain[i-1]].Label)))
			}
			line += stroke + next[chain[i-1]].Label + "▶"
		}
		if i == head {
			left = ansi.StringWidth(line)
		}
		if i == tail {
			right = ansi.StringWidth(line)
		}
		line += label
	}
	if ansi.StringWidth(line) <= width && right-left >= ansi.StringWidth(returnLabel)+2 {
		span := right - left - 1
		middle := strings.Repeat(" ", left) + "↑" + strings.Repeat(" ", span) + "│"
		bottom := strings.Repeat(" ", left) + "╰" + returnLabel + strings.Repeat(returnStroke, span-ansi.StringWidth(returnLabel)) + "╯"
		return line + "\n" + middle + "\n" + bottom, progress
	}
	// Reflow as a forward chain plus an explicit labeled return edge. Never
	// truncate a whole graph row: doing so would silently erase dependencies.
	var lines []string
	for i, label := range labels {
		lines = append(lines, "    "+ansi.Truncate(label, max(1, width-4), "…"))
		if i+1 < len(labels) {
			lines = append(lines, "    "+ansi.Truncate("↓ "+next[chain[i]].Label, max(1, width-4), "…"))
		}
	}
	back := loop.From + " " + returnStroke + strings.TrimSpace(returnLabel) + "▶ " + loop.To
	lines = append(lines, "    "+wrapLine(back, max(1, width-6), "    "))
	return strings.Join(lines, "\n"), progress
}

func branchJoinGraph(stages [][]*Node, statuses map[string]Status, width int) (string, bool) {
	if len(stages) != 3 || len(stages[0]) != 1 || len(stages[1]) < 2 || len(stages[2]) != 1 {
		return "", false
	}
	label := func(n *Node) string {
		if statuses == nil {
			return n.ID
		}
		return progressGlyph(statuses[n.ID]) + " " + n.ID
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

func wrapLine(s string, width int, indent string) string {
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
func (b *Blueprint) PreviewSteps(width int) string {
	if width <= 0 {
		width = 108
	}
	seen := make(map[string]bool)
	var lines []string
	for _, n := range b.topologicalNodes() {
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

func (b *Blueprint) dependencyList(statuses map[string]Status) string {
	var sb strings.Builder
	for _, n := range b.topologicalNodes() {
		if statuses == nil {
			fmt.Fprintf(&sb, "  %s", n.ID)
		} else {
			status := statuses[n.ID]
			if status == "" {
				status = StatusPending
			}
			fmt.Fprintf(&sb, "  %s %s", progressGlyph(status), n.ID)
		}
		if len(n.upstream) > 0 {
			sb.WriteString("  ← ")
			for i, edge := range n.upstream {
				if i > 0 {
					sb.WriteString(", ")
				}
				sb.WriteString(edge.From)
				if edge.Label != "" {
					fmt.Fprintf(&sb, " [%s]", edge.Label)
				}
			}
		}
		sb.WriteByte('\n')
	}
	return strings.TrimSuffix(sb.String(), "\n")
}

// topologicalNodes keeps the view in execution order even if markdown
// sections were written in another order.
func (b *Blueprint) topologicalNodes() []*Node {
	ordered := make([]*Node, 0, len(b.Nodes))
	seen := make(map[string]bool, len(b.Nodes))
	for len(ordered) < len(b.Nodes) {
		advanced := false
		for _, n := range b.Nodes {
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

// FinalOutput returns successful sink answers without synthesizing new content.
// A sole answer stands alone; multiple answers retain definition-order labels.
func (r *Result) FinalOutput(b *Blueprint) string {
	var answers []string
	var ids []string
	for _, n := range b.Nodes {
		res := r.Nodes[n.ID]
		if res == nil {
			continue
		}
		text := strings.TrimSpace(res.Output)
		if len(n.downstream) > 0 || res.Status != StatusSucceeded || text == "" {
			continue
		}
		answers = append(answers, text)
		ids = append(ids, n.ID)
	}
	if len(answers) == 1 {
		return answers[0]
	}
	for i := range answers {
		answers[i] = "## " + ids[i] + "\n" + answers[i]
	}
	return strings.Join(answers, "\n\n")
}

// Summary renders the run for the conversation that launched it: one status
// line per node, then the output of every sink node that succeeded. Sinks
// are the flow's product; intermediate outputs stay in the transcripts.
func (r *Result) Summary(b *Blueprint) string {
	var sb strings.Builder
	verdict := "succeeded"
	if r.Failed(b) {
		verdict = "failed"
	}
	fmt.Fprintf(&sb, "flow %s %s in %s\n", b.Name, verdict, r.Duration.Round(time.Second))
	for _, n := range b.Nodes {
		res := r.Nodes[n.ID]
		fmt.Fprintf(&sb, "  %s %s", statusGlyph(res.Status), n.ID)
		if res.Duration > 0 {
			fmt.Fprintf(&sb, " (%s)", res.Duration.Round(time.Second))
		}
		if res.Err != nil {
			fmt.Fprintf(&sb, ": %v", res.Err)
		}
		sb.WriteString("\n")
	}
	for _, n := range b.Nodes {
		res := r.Nodes[n.ID]
		if len(n.downstream) > 0 || res.Status != StatusSucceeded || strings.TrimSpace(res.Output) == "" {
			continue
		}
		fmt.Fprintf(&sb, "\n## %s\n%s\n", n.ID, strings.TrimSpace(res.Output))
	}
	return sb.String()
}

// The host styles ◉ as a breathing filled circle; completed circles stay static.
func progressGlyph(s Status) string {
	switch s {
	case StatusRunning:
		return "◉"
	case StatusSucceeded:
		return "●"
	case StatusFailed:
		return "● Failed"
	case StatusSkipped:
		return "○ Blocked"
	case StatusOmitted:
		return "○ Not selected"
	default:
		return "○"
	}
}

func statusGlyph(s Status) string {
	switch s {
	case StatusSucceeded:
		return "✓"
	case StatusFailed:
		return "✗"
	case StatusSkipped:
		return "↷"
	case StatusOmitted:
		return "⊘"
	case StatusRunning:
		return "●"
	default:
		return "○"
	}
}
