package input

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/app/kit"
)

// renderDAG draws nodes at stable topological coordinates, with real branch and
// join connectors. The list renderer remains preferable when labels won't fit.
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
		for _, n := range v.plan.Nodes {
			if levels[n.ID] == level {
				ids = append(ids, n.ID)
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
		old := rows[y][x]
		if old != ' ' && old != r {
			rows[y][x] = '┼'
		} else {
			rows[y][x] = r
		}
	}
	for _, e := range v.plan.Edges {
		a, b := positions[e.From], positions[e.To]
		bus := a.y + 2
		for y := a.y + 1; y <= bus; y++ {
			put(a.x, y, '│')
		}
		for x := min(a.x, b.x); x <= max(a.x, b.x); x++ {
			put(x, bus, '─')
		}
		for y := bus; y < b.y; y++ {
			put(b.x, y, '│')
		}
		if b.y > 0 {
			rows[b.y-1][b.x] = '▼'
		}
	}
	// Overlay styled labels after drawing connectors so edges never erase nodes.
	overlays := map[int]map[int]string{}
	for _, n := range v.plan.Nodes {
		p := positions[n.ID]
		label := v.graphNode(n.ID)
		start := max(0, p.x-ansi.StringWidth(label)/2)
		if overlays[p.y] == nil {
			overlays[p.y] = map[int]string{}
		}
		overlays[p.y][start] = label
	}
	lines := make([]string, len(rows))
	for y, row := range rows {
		var b strings.Builder
		for x := 0; x < width; {
			if label, ok := overlays[y][x]; ok {
				b.WriteString(label)
				x += ansi.StringWidth(label)
			} else {
				b.WriteRune(row[x])
				x++
			}
		}
		lines[y] = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Separator).Render(strings.TrimRight(b.String(), " "))
	}
	// Conditional labels and long edges remain explicit, not inferred from color.
	var labels []string
	for _, e := range v.plan.Edges {
		if e.Label != "" {
			labels = append(labels, e.From+" → "+e.To+" ["+e.Label+"]")
		}
	}
	if len(labels) > 0 {
		lines = append(lines, strings.Join(labels, " · "))
	}
	budget := max(3, v.height-15)
	if len(lines) > budget {
		p := positions[v.nodeID()]
		start := max(0, min(p.y-budget/2, len(lines)-budget))
		lines = lines[start : start+budget]
	}
	return strings.Join(lines, "\n")
}
func (v *WorkflowView) renderDependencyList(levels map[string]int) string {
	var lines []string
	for _, n := range v.plan.Nodes {
		var deps []string
		for _, e := range n.Upstream() {
			deps = append(deps, e.From)
		}
		suffix := ""
		if len(deps) > 0 {
			suffix = " ← " + strings.Join(deps, ", ")
		}
		lines = append(lines, strings.Repeat("  ", min(levels[n.ID], 4))+v.graphNode(n.ID)+suffix)
	}
	budget := max(3, v.height-15)
	if len(lines) > budget {
		start := max(0, min(v.selected-budget/2, len(lines)-budget))
		lines = lines[start : start+budget]
	}
	return strings.Join(lines, "\n")
}
