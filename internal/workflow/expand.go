package workflow

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// itemName is the template key holding a for_each worker's label: the item's
// `name` field when it has one, otherwise its position.
const itemName = "item.name"

// plan reads the JSON plan a for_each node fans out over. The orchestrator is
// a model, so its output is prose around the JSON rather than JSON; the first
// value that decodes wins.
func plan(n *Node, scope map[string]string) ([]map[string]string, error) {
	src, field := n.forEachNode, n.forEachField
	value, err := extractJSON(scope[src])
	if err != nil {
		return nil, fmt.Errorf("for_each %s: %w", n.ForEach, err)
	}
	if field != "" {
		obj, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("for_each %s: %s did not return a JSON object", n.ForEach, src)
		}
		if value, ok = obj[field]; !ok {
			return nil, fmt.Errorf("for_each %s: the plan has no %q field", n.ForEach, field)
		}
	}
	list, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("for_each %s: the plan is not a JSON array", n.ForEach)
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("for_each %s: the plan is empty", n.ForEach)
	}
	// The bound is what makes dynamic fan-out approvable, so an oversized
	// plan fails rather than silently losing the items past the cap.
	if len(list) > n.MaxWorkers {
		return nil, fmt.Errorf("for_each %s: the plan has %d items but max_workers is %d", n.ForEach, len(list), n.MaxWorkers)
	}

	items := make([]map[string]string, len(list))
	for i, raw := range list {
		items[i] = itemFields(raw)
		if items[i][itemName] == "" {
			items[i][itemName] = "#" + strconv.Itoa(i+1)
		}
	}
	return items, nil
}

// itemFields flattens one plan item into template keys: {{item}} is the item
// itself, and {{item.key}} each field of an object item.
func itemFields(raw any) map[string]string {
	obj, ok := raw.(map[string]any)
	if !ok {
		return map[string]string{"item": scalar(raw)}
	}
	fields := map[string]string{"item": compact(obj)}
	for k, v := range obj {
		fields["item."+k] = scalar(v)
	}
	return fields
}

func scalar(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return compact(v)
	}
}

// compact renders a non-string field the way the plan wrote it. Everything
// here came out of a JSON decode, so marshalling it back cannot fail.
func compact(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// extractJSON decodes the first complete JSON object or array in s, ignoring
// whatever the model wrote around it.
func extractJSON(s string) (any, error) {
	for i, r := range s {
		if r != '{' && r != '[' {
			continue
		}
		// UseNumber keeps a number as the literal the plan wrote. Without it
		// every number becomes a float64, so an id of 1000000 reaches the
		// worker as "1e+06" and anything past 2^53 is silently rounded.
		dec := json.NewDecoder(strings.NewReader(s[i:]))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err == nil {
			return v, nil
		}
	}
	return nil, fmt.Errorf("the output holds no JSON plan")
}

// Bounds reports the worst case a run can reach: how many subagent turns it
// can start, and the fan-out bound of each for_each node. The approval
// dialog states this, which is the whole reason dynamic fan-out is allowed.
func (w *Workflow) Bounds() (turns int, fanOut []string) {
	for _, n := range w.Nodes {
		if n.ForEach == "" {
			turns++
			continue
		}
		turns += n.MaxWorkers
		fanOut = append(fanOut, fmt.Sprintf("%s: up to %d workers over %s", n.ID, n.MaxWorkers, n.ForEach))
	}
	for _, l := range w.Loops {
		fanOut = append(fanOut, l.String())
	}
	return turns, fanOut
}

// Loop is a bounded back edge: From --> To, taken while From's output equals
// Label, at most Rounds times. Parsing unrolls it into a plain chain, so the
// scheduler never learns that a loop existed.
type Loop struct {
	From, To string
	Label    string
	Rounds   int
}

// String renders the bound for the approval dialog.
func (l Loop) String() string {
	return fmt.Sprintf("%s/%s: up to %d rounds", l.To, l.From, l.Rounds)
}

// checkLoops resolves each loop's body — the nodes on a forward path from
// its head to its tail — and rejects the shapes the unroller cannot turn back
// into a plain DAG.
func (w *Workflow) checkLoops(forward []Edge) error {
	down := map[string][]string{}
	up := map[string][]string{}
	for _, e := range forward {
		down[e.From] = append(down[e.From], e.To)
		up[e.To] = append(up[e.To], e.From)
	}
	w.loopOf = map[string]*Loop{}
	for i := range w.Loops {
		l := &w.Loops[i]
		fromHead, toTail := reach(l.To, down), reach(l.From, up)
		if !fromHead[l.From] {
			return fmt.Errorf("back edge %s -->|%s x%d| %s: %s is not an ancestor of %s, so there is no loop to bound",
				l.From, l.Label, l.Rounds, l.To, l.To, l.From)
		}
		inBody := func(id string) bool { return fromHead[id] && toTail[id] }
		for _, n := range w.Nodes {
			if !inBody(n.ID) {
				continue
			}
			if prev := w.loopOf[n.ID]; prev != nil {
				return fmt.Errorf("node %s is inside two loops (%s and %s); nested rounds are a cartesian explosion", n.ID, prev, l)
			}
			w.loopOf[n.ID] = l
			if n.ForEach != "" {
				return fmt.Errorf("node %s has for_each inside the loop %s; dynamic fan-out times iteration is a cartesian explosion", n.ID, l)
			}
			// Everything the body reads from outside must enter through the
			// loop's head, or a later round would re-read a node that ran once.
			for _, from := range up[n.ID] {
				if n.ID != l.To && !inBody(from) {
					return fmt.Errorf("node %s is inside the loop %s but %s --> %s enters the body from outside; route it through %s", n.ID, l, from, n.ID, l.To)
				}
			}
		}
	}
	return nil
}

// unroll replaces each loop's body with one copy per round, wired
// head-to-tail, and gives every round its own escape edges. What is left is
// an ordinary DAG.
func (w *Workflow) unroll() {
	if len(w.Loops) == 0 {
		return
	}
	inBody := w.loopOf
	instance := func(id string, round int) string {
		if inBody[id] == nil {
			return id
		}
		return fmt.Sprintf("%s#%d", id, round)
	}

	var nodes []*Node
	for _, n := range w.Nodes {
		l := inBody[n.ID]
		if l == nil {
			nodes = append(nodes, n)
			continue
		}
		for round := 1; round <= l.Rounds; round++ {
			clone := *n
			clone.ID = instance(n.ID, round)
			clone.tail = n.ID == l.From
			nodes = append(nodes, &clone)
		}
	}

	var edges []Edge
	for _, e := range w.Edges {
		from, to := inBody[e.From], inBody[e.To]
		rounds := 1
		if from != nil {
			rounds = from.Rounds
		}
		for round := 1; round <= rounds; round++ {
			// Within a body, round i stays in round i. Anywhere else — into a
			// body from outside, out to a plain node, into the head of a
			// second loop — the target is entered fresh at its round 1, so a
			// following loop starts from the top however long this one ran.
			target := 1
			if to == from {
				target = round
			}
			edges = append(edges, Edge{From: instance(e.From, round), To: instance(e.To, target), Label: e.Label})
		}
	}
	// The back edge becomes the chain: round i's tail feeds round i+1's head.
	// The last round has nowhere to go on a retry, which is what exhaustion
	// means; the scheduler fails that round when its answer takes no edge.
	for _, l := range w.Loops {
		for round := 1; round < l.Rounds; round++ {
			edges = append(edges, Edge{From: instance(l.From, round), To: instance(l.To, round+1), Label: l.Label})
		}
	}

	w.Nodes, w.Edges, w.byID = nodes, edges, map[string]*Node{}
	for _, n := range nodes {
		n.upstream, n.downstream = nil, nil
		w.byID[n.ID] = n
	}
	w.link(edges)
}

// reach returns every node reachable from start, inclusive.
func reach(start string, next map[string][]string) map[string]bool {
	seen := map[string]bool{start: true}
	queue := []string{start}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, to := range next[id] {
			if !seen[to] {
				seen[to] = true
				queue = append(queue, to)
			}
		}
	}
	return seen
}
