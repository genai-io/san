// Package workflow parses and runs a workflow: a declarative acyclic graph
// whose nodes are one subagent turn each, written as markdown with a mermaid
// flowchart for the topology (docs/design/proposals/0001-workflow-orchestration.md).
//
// The package imports nothing from San. Node execution enters through the
// NodeRunner seam so the host decides what a node is; the package can move to
// sdk-go once the schema settles.
package workflow

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Workflow is a parsed, validated definition.
type Workflow struct {
	Name        string
	Description string
	MaxParallel int
	Nodes       []*Node // in definition order, loop bodies unrolled
	Edges       []Edge
	Loops       []Loop

	byID   map[string]*Node
	loopOf map[string]*Loop // body node id → its loop, filled by checkLoops
}

// Node is one section of the definition: one subagent turn.
type Node struct {
	ID string
	// Base is the id as written in the graph. It differs from ID only for a
	// loop instance (`draft#2`), and it is what templates name: `{{draft}}`
	// inside a later round reads the round before it.
	Base string
	// Config holds the host-facing keys under the heading (agent, mode,
	// model). The engine reads none of them.
	Config map[string]string
	Prompt string
	// ContinueOnError lets this node's failure leave the workflow running:
	// downstream still runs, with {{id}} rendered empty.
	ContinueOnError bool
	// ForEach names the plan this node fans out over, as "id" or "id.field"
	// where id is an ancestor. Empty for an ordinary node.
	ForEach string
	// MaxWorkers bounds that fan-out. Required with ForEach — the whole
	// point is that the worst case is known before launch.
	MaxWorkers int
	// forEachNode and forEachField are ForEach split once, here, so the
	// regexp runs in one place and nothing downstream indexes an unchecked
	// match.
	forEachNode, forEachField string
	// tail marks a copy of the node that carries a back edge — the one node
	// whose author wrote down every answer it may give. See unanswered.
	tail bool

	upstream   []Edge
	downstream []Edge
}

// Edge is one arrow of the flowchart. A non-empty Label makes it
// conditional: To receives From only when From's trimmed output equals Label.
type Edge struct {
	From, To, Label string
}

// Node returns the node with the given id.
func (w *Workflow) Node(id string) (*Node, bool) {
	n, ok := w.byID[id]
	return n, ok
}

// Upstream returns the edges into the node.
func (n *Node) Upstream() []Edge { return n.upstream }

// Downstream returns the edges out of the node.
func (n *Node) Downstream() []Edge { return n.downstream }

const defaultMaxParallel = 4

// maxRounds caps a back edge's xN. An evaluator-optimizer loop that has not
// converged in ten rounds will not converge in a hundred; the cap exists so
// that the worst case stays something a person can be asked to approve.
const maxRounds = 10

// configKeys are the keys accepted under a node heading. Anything else that
// looks like `key: value` on the first lines is a typo, not prompt text.
var configKeys = []string{"agent", "mode", "model", "continue_on_error", "for_each", "max_workers"}

var (
	idRe       = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
	arrowRe    = regexp.MustCompile(`\s*-->\s*(?:\|([^|]*)\|)?\s*`)
	headingRe  = regexp.MustCompile(`^##\s+(\S+)\s*$`)
	configRe   = regexp.MustCompile(`^([a-z_]+):\s*(.*)$`)
	forEachRe  = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_-]*)(?:\.([A-Za-z_][A-Za-z0-9_-]*))?$`)
	boundRe    = regexp.MustCompile(`^(.*\S)\s+x(\d+)$`)
	templateRe = regexp.MustCompile(`\{\{\s*([A-Za-z][A-Za-z0-9_.-]*)\s*\}\}`)
)

// Parse reads a definition and validates it: one mermaid block, one section
// per graph node and vice versa, no cycle, templates referencing only
// connected upstreams. Every problem found is reported at once.
func Parse(src string) (*Workflow, error) {
	w := &Workflow{MaxParallel: defaultMaxParallel, byID: map[string]*Node{}}

	body, err := w.parseFrontmatter(src)
	if err != nil {
		return nil, err
	}
	graph, rest, err := splitMermaid(body)
	if err != nil {
		return nil, err
	}
	declared, err := w.parseGraph(graph)
	if err != nil {
		return nil, err
	}
	if err := w.parseSections(rest); err != nil {
		return nil, err
	}
	if err := w.validate(declared); err != nil {
		return nil, err
	}
	w.unroll()
	return w, nil
}

func (w *Workflow) parseFrontmatter(src string) (string, error) {
	src = strings.TrimLeft(src, "\n")
	if !strings.HasPrefix(src, "---\n") {
		return src, nil
	}
	end := strings.Index(src[4:], "\n---")
	if end < 0 {
		return "", fmt.Errorf("frontmatter is not closed")
	}
	var fm struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
		MaxParallel int    `yaml:"max_parallel"`
	}
	if err := yaml.Unmarshal([]byte(src[4:4+end]), &fm); err != nil {
		return "", fmt.Errorf("frontmatter: %w", err)
	}
	w.Name, w.Description = fm.Name, fm.Description
	if fm.MaxParallel < 0 {
		return "", fmt.Errorf("max_parallel must be at least 1")
	}
	if fm.MaxParallel > 0 {
		w.MaxParallel = fm.MaxParallel
	}
	rest := src[4+end+4:]
	return strings.TrimPrefix(rest, "\n"), nil
}

// splitMermaid returns the body of the single ```mermaid fence and the rest
// of the document with the fence removed.
func splitMermaid(body string) (graph, rest string, err error) {
	lines := strings.Split(body, "\n")
	var out, fence []string
	inFence, seen := false, false
	for _, line := range lines {
		switch {
		case inFence && strings.HasPrefix(strings.TrimSpace(line), "```"):
			inFence = false
		case inFence:
			fence = append(fence, line)
		case strings.TrimSpace(line) == "```mermaid":
			if seen {
				return "", "", fmt.Errorf("more than one mermaid block; the topology is one flowchart")
			}
			inFence, seen = true, true
		default:
			out = append(out, line)
		}
	}
	if inFence {
		return "", "", fmt.Errorf("mermaid block is not closed")
	}
	if !seen {
		return "", "", fmt.Errorf("no ```mermaid block: the topology is a flowchart")
	}
	return strings.Join(fence, "\n"), strings.Join(out, "\n"), nil
}

// parseGraph reads the mermaid subset: a `flowchart` header, then edge lines
// built from bare ids, `-->`, `&` and `|LABEL|`. It returns the ids the graph
// declares, in first-seen order.
func (w *Workflow) parseGraph(graph string) ([]string, error) {
	var declared []string
	seen := map[string]bool{}
	declare := func(id string) {
		if !seen[id] {
			seen[id] = true
			declared = append(declared, id)
		}
	}
	edgeSeen := map[[2]string]bool{}
	header := false
	for raw := range strings.SplitSeq(graph, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "%%") {
			continue
		}
		if !header {
			f := strings.Fields(line)
			if len(f) != 2 || (f[0] != "flowchart" && f[0] != "graph") {
				return nil, fmt.Errorf("mermaid block must start with `flowchart LR`, got %q", line)
			}
			header = true
			continue
		}
		groups, labels, err := splitArrows(line)
		if err != nil {
			return nil, err
		}
		for i, g := range groups {
			for _, id := range g {
				declare(id)
			}
			if i == 0 {
				continue
			}
			for _, from := range groups[i-1] {
				for _, to := range g {
					key := [2]string{from, to}
					if edgeSeen[key] {
						return nil, fmt.Errorf("edge %s --> %s appears twice", from, to)
					}
					edgeSeen[key] = true
					w.Edges = append(w.Edges, Edge{From: from, To: to, Label: labels[i-1]})
				}
			}
		}
	}
	if !header {
		return nil, fmt.Errorf("mermaid block is empty")
	}
	return declared, nil
}

// splitArrows turns `a & b -->|X| c --> d` into groups [[a b] [c] [d]] and
// labels [X ""].
func splitArrows(line string) (groups [][]string, labels []string, err error) {
	matches := arrowRe.FindAllStringSubmatchIndex(line, -1)
	pos := 0
	var segments []string
	for _, m := range matches {
		segments = append(segments, line[pos:m[0]])
		label := ""
		if m[2] >= 0 {
			label = strings.TrimSpace(line[m[2]:m[3]])
			if label == "" {
				return nil, nil, fmt.Errorf("empty edge label in %q", line)
			}
		}
		labels = append(labels, label)
		pos = m[1]
	}
	segments = append(segments, line[pos:])
	for _, seg := range segments {
		var g []string
		for id := range strings.SplitSeq(seg, "&") {
			id = strings.TrimSpace(id)
			if !idRe.MatchString(id) {
				return nil, nil, fmt.Errorf("unsupported mermaid syntax %q in %q: nodes are bare ids, edges use -->, & and |LABEL|", id, line)
			}
			g = append(g, id)
		}
		groups = append(groups, g)
	}
	return groups, labels, nil
}

// parseSections reads every `## id` section: leading `key: value` lines are
// config, everything after them is the prompt. Text before the first heading
// is ignored so a definition can open with prose.
func (w *Workflow) parseSections(rest string) error {
	var cur *Node
	var buf []string
	inFence := false
	flush := func() error {
		if cur == nil {
			return nil
		}
		if err := cur.fill(buf); err != nil {
			return err
		}
		w.Nodes = append(w.Nodes, cur)
		w.byID[cur.ID] = cur
		cur, buf = nil, nil
		return nil
	}
	for line := range strings.SplitSeq(rest, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
		}
		if m := headingRe.FindStringSubmatch(line); m != nil && !inFence {
			if err := flush(); err != nil {
				return err
			}
			if _, dup := w.byID[m[1]]; dup {
				return fmt.Errorf("node %s is defined twice", m[1])
			}
			cur = &Node{ID: m[1], Base: m[1], Config: map[string]string{}}
			continue
		}
		if cur != nil {
			buf = append(buf, line)
		}
	}
	return flush()
}

func (n *Node) fill(lines []string) error {
	i := 0
	for ; i < len(lines); i++ {
		m := configRe.FindStringSubmatch(lines[i])
		if m == nil {
			break
		}
		key, value := m[1], strings.TrimSpace(m[2])
		switch key {
		case "continue_on_error":
			b, err := strconv.ParseBool(value)
			if err != nil {
				return fmt.Errorf("node %s: continue_on_error must be true or false", n.ID)
			}
			n.ContinueOnError = b
		case "for_each":
			n.ForEach = value
		case "max_workers":
			w, err := strconv.Atoi(value)
			if err != nil || w < 1 {
				return fmt.Errorf("node %s: max_workers must be a positive number", n.ID)
			}
			n.MaxWorkers = w
		case "mode":
			switch value {
			case "", "default", "explore", "edit":
			default:
				return fmt.Errorf("node %s: mode must be explore, edit, or default", n.ID)
			}
			n.Config[key] = value
		default:
			if !slices.Contains(configKeys, key) {
				return fmt.Errorf("node %s: unknown config key %q (accepted: %s); put a blank line before prompt text that starts with `word:`", n.ID, key, strings.Join(configKeys, ", "))
			}
			n.Config[key] = value
		}
	}
	n.Prompt = strings.TrimSpace(strings.Join(lines[i:], "\n"))

	switch {
	case n.ForEach == "" && n.MaxWorkers > 0:
		return fmt.Errorf("node %s: max_workers means nothing without for_each", n.ID)
	case n.ForEach != "" && n.MaxWorkers == 0:
		return fmt.Errorf("node %s: for_each requires max_workers — a plan that goes wrong must not start an unbounded number of subagents", n.ID)
	case n.ForEach != "":
		m := forEachRe.FindStringSubmatch(n.ForEach)
		if m == nil {
			return fmt.Errorf("node %s: for_each must be `node` or `node.field`, got %q", n.ID, n.ForEach)
		}
		n.forEachNode, n.forEachField = m[1], m[2]
	}
	return nil
}

// isItemRef reports a template reference to the current for_each item.
func isItemRef(ref string) bool { return ref == "item" || strings.HasPrefix(ref, "item.") }

// validate cross-checks graph and sections, rejects cycles, and checks every
// template reference. Problems are collected so one round trip fixes them all.
func (w *Workflow) validate(declared []string) error {
	var problems []string
	for _, id := range declared {
		if _, ok := w.byID[id]; !ok {
			problems = append(problems, fmt.Sprintf("node %s is in the graph but has no `## %s` section", id, id))
		}
	}
	for _, n := range w.Nodes {
		if !slices.Contains(declared, n.ID) {
			problems = append(problems, fmt.Sprintf("section `## %s` is not in the graph", n.ID))
		}
		if n.Prompt == "" {
			problems = append(problems, fmt.Sprintf("node %s has no prompt", n.ID))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}

	// Linked as drawn, back edges included, so that {{review}} inside draft
	// resolves. Unrolling relinks the executable graph when there are loops;
	// without them the graph as drawn is the executable one.
	w.link(w.Edges)

	// A labelled edge carrying a bound is a declared back edge; it is held
	// out of the acyclic graph and unrolled later. Everything else must
	// leave a DAG behind.
	var forward []Edge
	for _, e := range w.Edges {
		if m := boundRe.FindStringSubmatch(e.Label); m != nil {
			// Checked before unroll allocates anything: parsing runs while
			// the approval dialog is built. An overflowing number parses as
			// MaxInt and is refused by the same test.
			rounds, _ := strconv.Atoi(m[2])
			if rounds < 1 || rounds > maxRounds {
				return fmt.Errorf("back edge %s --> %s: x%s must be between 1 and %d rounds", e.From, e.To, m[2], maxRounds)
			}
			w.Loops = append(w.Loops, Loop{From: e.From, To: e.To, Label: m[1], Rounds: rounds})
			continue
		}
		forward = append(forward, e)
	}
	w.Edges = forward
	if left := w.cycle(forward); len(left) > 0 {
		return fmt.Errorf("graph has a cycle through %s; a back edge needs a bound, as in `%s -->|FAIL x3| %s`", strings.Join(left, ", "), left[len(left)-1], left[0])
	}
	if err := w.checkLoops(forward); err != nil {
		return err
	}

	for _, n := range w.Nodes {
		ancestors := w.ancestors(n)
		if n.ForEach != "" {
			if src := n.forEachNode; !ancestors[src] {
				problems = append(problems, fmt.Sprintf("node %s: for_each reads %s but the graph has no path %s --> … --> %s", n.ID, src, src, n.ID))
			}
		}
		for _, ref := range n.refs() {
			switch {
			case strings.HasPrefix(ref, "input."):
			case isItemRef(ref):
				if n.ForEach == "" {
					problems = append(problems, fmt.Sprintf("node %s references {{%s}} but has no for_each", n.ID, ref))
				}
			case !ancestors[ref]:
				problems = append(problems, fmt.Sprintf("node %s references {{%s}} but the graph has no path %s --> … --> %s", n.ID, ref, ref, n.ID))
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return nil
}

// link records each edge on both of its ends.
func (w *Workflow) link(edges []Edge) {
	for _, e := range edges {
		w.byID[e.From].downstream = append(w.byID[e.From].downstream, e)
		w.byID[e.To].upstream = append(w.byID[e.To].upstream, e)
	}
}

// cycle runs Kahn's algorithm over the given edges and returns the nodes it
// could not remove — empty for a DAG.
func (w *Workflow) cycle(edges []Edge) []string {
	indeg := map[string]int{}
	out := map[string][]Edge{}
	for _, e := range edges {
		indeg[e.To]++
		out[e.From] = append(out[e.From], e)
	}
	var queue []string
	for _, n := range w.Nodes {
		if indeg[n.ID] == 0 {
			queue = append(queue, n.ID)
		}
	}
	removed := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		removed++
		for _, e := range out[id] {
			indeg[e.To]--
			if indeg[e.To] == 0 {
				queue = append(queue, e.To)
			}
		}
	}
	if removed == len(w.Nodes) {
		return nil
	}
	var left []string
	for _, n := range w.Nodes {
		if indeg[n.ID] > 0 {
			left = append(left, n.ID)
		}
	}
	return left
}

// refs lists the template references in the prompt, in order, deduplicated.
func (n *Node) refs() []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range templateRe.FindAllStringSubmatch(n.Prompt, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

// ancestors returns every node with a path into n. Templates may reference
// any of them: data flows along edges, not just across the last one.
func (w *Workflow) ancestors(n *Node) map[string]bool {
	seen := map[string]bool{}
	var walk func(*Node)
	walk = func(m *Node) {
		for _, e := range m.upstream {
			if !seen[e.From] {
				seen[e.From] = true
				walk(w.byID[e.From])
			}
		}
	}
	walk(n)
	return seen
}

// render substitutes {{id}} with an ancestor's output, {{input.key}} with the
// trigger parameter, and {{item}} / {{item.key}} with the for_each item;
// anything unset renders empty.
func render(prompt string, outputs, inputs, item map[string]string) string {
	return templateRe.ReplaceAllStringFunc(prompt, func(m string) string {
		ref := templateRe.FindStringSubmatch(m)[1]
		switch key, isInput := strings.CutPrefix(ref, "input."); {
		case isInput:
			return inputs[key]
		case isItemRef(ref):
			return item[ref]
		default:
			return outputs[ref]
		}
	})
}
