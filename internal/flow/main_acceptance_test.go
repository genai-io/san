package flow

import (
	"strings"
	"testing"
)

func TestMainLoopAcceptanceBoundary(t *testing.T) {
	cases := []struct {
		name, graph, sections string
		valid                 bool
	}{
		{name: "critic decision tail", graph: "decision -->|PASS| done", sections: "## done\naccepted\n", valid: true},
		{name: "interior PASS", graph: "critic -->|PASS| done\ndecision -->|PASS| done", sections: "## done\naccepted\n"},
		{name: "unconditional exit", graph: "decision --> done", sections: "## done\naccepted\n"},
		{name: "retry label escape", graph: "decision -->|REVISE| done", sections: "## done\naccepted\n"},
		{name: "missing acceptance"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := "```mermaid\nflowchart LR\nwriter --> critic --> decision\ndecision -->|REVISE x3| writer\n" + tc.graph + "\n```\n## writer\nexecutor: main\n\nwrite\n## critic\nreview {{writer}}\n## decision\nverdict {{critic}}\n" + tc.sections
			_, err := Parse(source)
			if (err == nil) != tc.valid {
				t.Fatalf("main loop validity=%v, error=%v", tc.valid, err)
			}
			// These graph shapes remain valid for ordinary subagent loops.
			_, err = Parse(strings.Replace(source, "executor: main", "executor: subagent", 1))
			if err != nil {
				t.Fatalf("subagent behavior changed: %v", err)
			}
		})
	}
}
