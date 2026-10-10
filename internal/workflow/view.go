package workflow

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

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
	TextPending        bool // streamed text not yet accepted by the model loop
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
