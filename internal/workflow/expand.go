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
	return turns, fanOut
}
