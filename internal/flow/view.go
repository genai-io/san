package flow

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// Title returns a display label without changing the authored name.
func (w *Blueprint) Title() string {
	words := strings.FieldsFunc(w.Name, func(r rune) bool {
		return r == '_' || r == '-' || unicode.IsSpace(r)
	})
	// Split CamelCase and acronym-to-word boundaries for display only. Keep
	// acronyms intact (HTTPReview becomes HTTP Review, not H T T P Review).
	var splitWords []string
	for _, word := range words {
		runes := []rune(word)
		// Keep mixed-case acronyms such as iOS together rather than
		// treating their single lowercase prefix as a separate word.
		mixedAcronym := len(runes) > 1 && unicode.IsLower(runes[0])
		for _, r := range runes[1:] {
			mixedAcronym = mixedAcronym && unicode.IsUpper(r)
		}
		if mixedAcronym {
			splitWords = append(splitWords, word)
			continue
		}
		var title strings.Builder
		for j, r := range runes {
			if j > 0 && unicode.IsUpper(r) {
				previous := runes[j-1]
				wordStart := unicode.IsLower(previous) || unicode.IsDigit(previous)
				acronymEnd := unicode.IsUpper(previous) && j+1 < len(runes) && unicode.IsLower(runes[j+1])
				if wordStart || acronymEnd {
					title.WriteByte(' ')
				}
			}
			title.WriteRune(r)
		}
		splitWords = append(splitWords, strings.Fields(title.String())...)
	}
	words = splitWords
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
func (w *Blueprint) CompactProgressView(statuses map[string]Status, width int) string {
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
	progress := fmt.Sprintf("%d/%d", finished, len(w.Nodes))
	if statuses == nil {
		unit := "steps"
		if len(w.Nodes) == 1 {
			unit = "step"
		}
		progress = fmt.Sprintf("%d %s", len(w.Nodes), unit)
	}
	// Align the title and progress with the graph's left edge.
	available := max(1, width-4)
	suffix := " · " + progress
	nameWidth := available - ansi.StringWidth(suffix)
	header := progress
	if nameWidth > 0 {
		header = ansi.Truncate(w.Title(), nameWidth, "…") + suffix
	}
	header = strings.Repeat(" ", min(4, max(0, width-1))) + ansi.Truncate(header, available, "…")
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
							label += " " + progressGlyph(statuses[n.ID])
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
		lines = append(lines, "    "+wrapLine(strings.TrimPrefix(line, "  "), max(1, width-4), "      "))
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
		return n.ID + " " + progressGlyph(statuses[n.ID])
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
func (w *Blueprint) PreviewSteps(width int) string {
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

func (w *Blueprint) dependencyList(statuses map[string]Status) string {
	var b strings.Builder
	for _, n := range w.topologicalNodes() {
		if statuses == nil {
			fmt.Fprintf(&b, "  %s", n.ID)
		} else {
			status := statuses[n.ID]
			if status == "" {
				status = StatusPending
			}
			fmt.Fprintf(&b, "  %s %s", progressGlyph(status), n.ID)
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
func (w *Blueprint) topologicalNodes() []*Node {
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

// Summary renders the run for the conversation that launched it: one status
// line per node, then the output of every sink node that succeeded. Sinks
// are the flow's product; intermediate outputs stay in the transcripts.
func (r *Result) Summary(w *Blueprint) string {
	var b strings.Builder
	verdict := "succeeded"
	if r.Failed(w) {
		verdict = "failed"
	}
	fmt.Fprintf(&b, "flow %s %s in %s\n", w.Name, verdict, r.Duration.Round(time.Second))
	for _, n := range w.Nodes {
		res := r.Nodes[n.ID]
		fmt.Fprintf(&b, "  %s %s", statusGlyph(res.Status), n.ID)
		if res.Duration > 0 {
			fmt.Fprintf(&b, " (%s)", res.Duration.Round(time.Second))
		}
		if res.Err != nil {
			fmt.Fprintf(&b, ": %v", res.Err)
		}
		b.WriteString("\n")
	}
	for _, n := range w.Nodes {
		res := r.Nodes[n.ID]
		if len(n.downstream) > 0 || res.Status != StatusSucceeded || strings.TrimSpace(res.Output) == "" {
			continue
		}
		fmt.Fprintf(&b, "\n## %s\n%s\n", n.ID, strings.TrimSpace(res.Output))
	}
	return b.String()
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
