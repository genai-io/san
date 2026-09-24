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
	Nodes       []*Node // in definition order
	Edges       []Edge

	byID map[string]*Node
}

// Node is one section of the definition: one subagent turn.
type Node struct {
	ID string
	// Config holds the host-facing keys under the heading (agent, mode,
	// model). The engine reads none of them.
	Config map[string]string
	Prompt string
	// ContinueOnError lets this node's failure leave the workflow running:
	// downstream still runs, with {{id}} rendered empty.
	ContinueOnError bool

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

// configKeys are the keys accepted under a node heading. Anything else that
// looks like `key: value` on the first lines is a typo, not prompt text.
var configKeys = map[string]bool{"agent": true, "mode": true, "model": true, "continue_on_error": true}

var (
	idRe       = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
	arrowRe    = regexp.MustCompile(`\s*-->\s*(?:\|([^|]*)\|)?\s*`)
	headingRe  = regexp.MustCompile(`^##\s+(\S+)\s*$`)
	configRe   = regexp.MustCompile(`^([a-z_]+):\s*(.*)$`)
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
	return w, w.validate(declared)
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
			cur = &Node{ID: m[1], Config: map[string]string{}}
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
		if !configKeys[m[1]] {
			return fmt.Errorf("node %s: unknown config key %q (accepted: agent, mode, model, continue_on_error); put a blank line before prompt text that starts with `word:`", n.ID, m[1])
		}
		n.Config[m[1]] = strings.TrimSpace(m[2])
	}
	n.Prompt = strings.TrimSpace(strings.Join(lines[i:], "\n"))
	if v, ok := n.Config["continue_on_error"]; ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("node %s: continue_on_error must be true or false", n.ID)
		}
		n.ContinueOnError = b
		delete(n.Config, "continue_on_error")
	}
	switch n.Config["mode"] {
	case "", "default", "explore", "edit":
	default:
		return fmt.Errorf("node %s: mode must be explore, edit, or default", n.ID)
	}
	return nil
}

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

	for _, e := range w.Edges {
		w.byID[e.From].downstream = append(w.byID[e.From].downstream, e)
		w.byID[e.To].upstream = append(w.byID[e.To].upstream, e)
	}
	if left := w.cycle(); len(left) > 0 {
		return fmt.Errorf("graph has a cycle through %s; a back edge needs a bound (not supported yet)", strings.Join(left, ", "))
	}

	for _, n := range w.Nodes {
		ancestors := w.ancestors(n)
		for _, ref := range n.refs() {
			if strings.HasPrefix(ref, "input.") {
				continue
			}
			if !ancestors[ref] {
				problems = append(problems, fmt.Sprintf("node %s references {{%s}} but the graph has no path %s --> … --> %s", n.ID, ref, ref, n.ID))
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return nil
}

// cycle runs Kahn's algorithm and returns the nodes it could not remove —
// empty for a DAG.
func (w *Workflow) cycle() []string {
	indeg := map[string]int{}
	for _, n := range w.Nodes {
		indeg[n.ID] = len(n.upstream)
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
		for _, e := range w.byID[id].downstream {
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

// render substitutes {{id}} with an ancestor's output and {{input.key}} with
// the trigger parameter; anything unset renders empty.
func render(prompt string, outputs, inputs map[string]string) string {
	return templateRe.ReplaceAllStringFunc(prompt, func(m string) string {
		ref := templateRe.FindStringSubmatch(m)[1]
		if key, ok := strings.CutPrefix(ref, "input."); ok {
			return inputs[key]
		}
		return outputs[ref]
	})
}
