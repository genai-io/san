package flow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestCompactLoopActiveRoutes(t *testing.T) {
	w := mustParse(t, strings.ReplaceAll(optimizer, "FAIL", "REVISE"))
	for _, tc := range []struct {
		statuses map[string]Status
		want     string
		retry    bool
	}{
		{nil, "[draft]", false},
		{map[string]Status{"review#1": StatusRunning}, "━▶[◉ review]", false},
		{map[string]Status{"draft#2": StatusRunning}, "REVISE ×1 ━", true},
		{map[string]Status{"review#2": StatusRunning}, "REVISE ×1 ─", true},
		{map[string]Status{"draft#3": StatusRunning}, "REVISE ×2 ━", true},
		{map[string]Status{"review#2": StatusSucceeded, "ship": StatusRunning}, "━PASS▶[◉ ship]", true},
	} {
		view := w.CompactProgressView(tc.statuses, 80)
		if !strings.Contains(view, tc.want) {
			t.Fatalf("missing %q:\n%s", tc.want, view)
		}
		if strings.Contains(view, "×") != tc.retry {
			t.Fatal(view)
		}
		if strings.Count(view, "\n") != 3 {
			t.Fatal(view)
		}
	}
}

func TestCompactLoopProgress(t *testing.T) {
	w := mustParse(t, strings.ReplaceAll(optimizer, "FAIL", "REVISE"))
	for _, tc := range []struct {
		name          string
		statuses      map[string]Status
		round, marker string
	}{
		{"preview", nil, "up to 3 rounds", "draft"},
		{"draft", map[string]Status{"draft#1": StatusRunning}, "round 1/3", "◉ draft"},
		{"review", map[string]Status{"draft#1": StatusSucceeded, "review#1": StatusRunning}, "round 1/3", "◉ review"},
		{"retry", map[string]Status{"draft#1": StatusSucceeded, "review#1": StatusSucceeded, "draft#2": StatusRunning}, "round 2/3", "○ review"},
		{"accepted", map[string]Status{"draft#2": StatusSucceeded, "review#2": StatusSucceeded, "draft#3": StatusOmitted, "review#3": StatusSkipped, "ship": StatusRunning}, "round 2/3", "◉ ship"},
		{"exhausted", map[string]Status{"review#3": StatusFailed}, "round 3/3", "● Failed review"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, width := range []int{32, 48, 80, 120} {
				view := w.CompactProgressView(tc.statuses, width)
				for _, want := range []string{tc.round, tc.marker, "PASS", "REVISE", "spec", "ship"} {
					if !strings.Contains(view, want) {
						t.Fatalf("missing %q:\n%s", want, view)
					}
				}
				if strings.Contains(view, "#") || view != w.CompactProgressView(tc.statuses, width) {
					t.Fatalf("unfolded or unstable:\n%s", view)
				}
				if tc.statuses == nil && strings.ContainsAny(view, "○●◉") {
					t.Fatal(view)
				}
				for line := range strings.SplitSeq(view, "\n") {
					if ansi.StringWidth(line) > width {
						t.Fatalf("width %d: %q", width, line)
					}
				}
				if width >= 80 && strings.Count(view, "\n") != 3 {
					t.Fatalf("not horizontal:\n%s", view)
				}
			}
		})
	}
}

func TestCompactLoopDynamicCounts(t *testing.T) {
	w := mustParse(t, "```mermaid\nflowchart LR\na --> b --> c\nc -->|REVISE x3| a\nc -->|PASS| result\n```\n## a\nA\n## b\nB\n## c\nC\n## result\nResult\n")
	statuses := map[string]Status{}
	steps := []struct{ id, count string }{
		{"a#1", "0/4"}, {"b#1", "1/4"}, {"c#1", "2/4"},
		{"a#2", "3/7"}, {"b#2", "4/7"}, {"c#2", "5/7"},
		{"a#3", "6/10"}, {"b#3", "7/10"}, {"c#3", "8/10"},
	}
	for i, step := range steps {
		if i > 0 {
			statuses[steps[i-1].id] = StatusSucceeded
		}
		statuses[step.id] = StatusRunning
		for _, width := range []int{80, 120} {
			header, _, _ := strings.Cut(w.CompactProgressView(statuses, width), "\n")
			if !strings.HasSuffix(header, step.count) {
				t.Fatalf("%s: %s", step.id, header)
			}
		}
	}
	statuses["c#3"], statuses["result"] = StatusFailed, StatusSkipped
	if view := w.CompactProgressView(statuses, 80); !strings.Contains(view, "round 3/3 · 10/10") {
		t.Fatal(view)
	}
	for _, accepted := range []int{1, 2} {
		statuses = map[string]Status{"result": StatusSucceeded}
		for _, n := range w.Nodes {
			if n.ID == "result" {
				continue
			}
			statuses[n.ID] = StatusOmitted
			for r := 1; r <= accepted; r++ {
				if strings.HasSuffix(n.ID, fmt.Sprintf("#%d", r)) {
					statuses[n.ID] = StatusSucceeded
				}
			}
		}
		want := fmt.Sprintf("round %d/3 · %d/%d", accepted, accepted*3+1, accepted*3+1)
		if view := w.CompactProgressView(statuses, 80); !strings.Contains(view, want) {
			t.Fatal(view)
		}
	}
}

func TestCompactLoopBranchFallback(t *testing.T) {
	source := strings.Replace(optimizer, "spec --> draft --> review", "spec --> draft --> review\n draft --> check --> review", 1) + "\n## check\nCheck\n"
	w := mustParse(t, source)
	view := w.CompactProgressView(nil, 120)
	if !strings.Contains(view, "review#1 ← draft#1, check#1") || !strings.Contains(view, "[FAIL]") || !strings.Contains(view, "[PASS]") {
		t.Fatal(view)
	}
}

func TestBlueprintTitle(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"InternalReview", "InternalReview"},
		{"flow_readonly_review", "Readonly review"},
		{"normal name", "Normal name"},
		{" FLOW--équipe\t检查 ", "Équipe 检查"},
		{"iOS-review", "IOS review"},
		{"flow", "Flow"},
		{"", "Untitled flow"},
		{" _-\t", "Untitled flow"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &Blueprint{Name: tc.name, Nodes: []*Node{{ID: "original_ID"}}}
			if got := w.Title(); got != tc.want {
				t.Fatalf("Title() = %q, want %q", got, tc.want)
			}
			if w.Name != tc.name || w.Nodes[0].ID != "original_ID" {
				t.Fatal("display title changed name or node ID")
			}
		})
	}
}

func TestCompactFlowHeaders(t *testing.T) {
	w := &Blueprint{Name: "flow_readonly_review", Nodes: []*Node{{ID: "a"}}}
	for _, tc := range []struct {
		statuses map[string]Status
		want     string
	}{
		{nil, "    Flow · Readonly review · 1 step"},
		{map[string]Status{}, "    Flow · Readonly review · 0/1"},
		{map[string]Status{"a": StatusPending}, "    Flow · Readonly review · 0/1"},
		{map[string]Status{"a": StatusRunning}, "    Flow · Readonly review · 0/1"},
		{map[string]Status{"a": StatusSucceeded}, "    Flow · Readonly review · 1/1"},
		{map[string]Status{"a": StatusFailed}, "    Flow · Readonly review · 1/1"},
		{map[string]Status{"a": StatusSkipped}, "    Flow · Readonly review · 1/1"},
		{map[string]Status{"a": StatusOmitted}, "    Flow · Readonly review · 1/1"},
	} {
		header, _, _ := strings.Cut(w.CompactProgressView(tc.statuses, 80), "\n")
		if header != tc.want {
			t.Errorf("header = %q, want %q", header, tc.want)
		}
	}
	w.Nodes = append(w.Nodes, &Node{ID: "b"})
	if header, _, _ := strings.Cut(w.CompactProgressView(nil, 80), "\n"); header != "    Flow · Readonly review · 2 steps" {
		t.Fatalf("plural preview header = %q", header)
	}
}

func TestCompactFlowHeaderPreservesProgressAtNarrowWidths(t *testing.T) {
	w := &Blueprint{Name: strings.Repeat("检查", 20), Nodes: []*Node{{ID: "a"}}}
	for _, width := range []int{14, 20, 30} {
		header, _, _ := strings.Cut(w.CompactProgressView(map[string]Status{}, width), "\n")
		if ansi.StringWidth(header) > width || !strings.HasSuffix(header, "0/1") {
			t.Errorf("header at width %d = %q", width, header)
		}
	}
}

func TestCompactFlowViewDrawsBranchAndJoinWithoutRepeatedDetails(t *testing.T) {
	w, err := Parse("---\nname: audit\n---\n```mermaid\nflowchart LR\n  map --> bugs & risk & lean --> report\n```\n\n## map\nmode: explore\n\nMap\n\n## bugs\nmode: explore\n\nBugs\n\n## risk\nmode: explore\n\nRisk\n\n## lean\nmode: explore\n\nLean\n\n## report\nmode: explore\n\nReport\n")
	if err != nil {
		t.Fatal(err)
	}
	preview := w.CompactProgressView(nil, 100)
	if !strings.Contains(preview, "map ─┬──▶ bugs") || !strings.Contains(preview, "risk") || !strings.Contains(preview, "lean") || !strings.Contains(preview, "──▶ report") || strings.Contains(preview, "explore") || strings.Count(preview, "\n") != 3 {
		t.Fatalf("preview is not compact:\n%s", preview)
	}
	running := w.CompactProgressView(map[string]Status{"map": StatusSucceeded, "bugs": StatusRunning}, 100)
	if !strings.Contains(running, "● map ─┬──▶ ◉ bugs") || !strings.Contains(running, "○ risk") || !strings.Contains(running, "○ report") {
		t.Fatalf("running topology:\n%s", running)
	}
}

func TestCompactFlowMarkersPrecedeNodeNames(t *testing.T) {
	ids := []string{"inventory", "correctness", "security", "maintainability", "summary"}
	statuses := map[string]Status{
		"inventory": StatusSucceeded, "correctness": StatusRunning,
		"security": StatusRunning, "maintainability": StatusRunning,
	}
	for _, tc := range []struct{ name, graph string }{
		{"chain", "inventory --> correctness --> security --> maintainability --> summary"},
		{"fork-join", "inventory --> correctness & security & maintainability --> summary"},
		{"parallel-stage", "inventory --> correctness & security --> maintainability --> summary"},
		{"conditional", "inventory --> correctness --> security --> maintainability --> summary\ninventory -->|YES| summary"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "```mermaid\nflowchart LR\n" + tc.graph + "\n```\n"
			for _, id := range ids {
				source += "\n## " + id + "\nInspect\n"
			}
			w, err := Parse(source)
			if err != nil {
				t.Fatal(err)
			}
			for _, width := range []int{48, 80, 120} {
				view := w.CompactProgressView(statuses, width)
				for _, id := range ids {
					glyph := progressGlyph(statuses[id])
					if !strings.Contains(view, glyph+" "+id) || strings.Contains(view, id+" "+glyph) {
						t.Fatalf("width %d misplaced %s status:\n%s", width, id, view)
					}
				}
				for line := range strings.SplitSeq(view, "\n") {
					if ansi.StringWidth(line) > width {
						t.Fatalf("width %d overflowed: %q", width, line)
					}
				}
			}
		})
	}
}

func TestPreviewStepsCountsChineseDisplayColumns(t *testing.T) {
	w, err := Parse("```mermaid\nflowchart LR\n  map\n```\n\n## map\n" + strings.Repeat("检查仓库与关键路径。", 10) + "\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, width := range []int{72, 108} {
		line := w.PreviewSteps(width)
		if got := ansi.StringWidth(line); got > width {
			t.Fatalf("preview step uses %d columns in %d-column view: %q", got, width, line)
		}
	}
}

func TestCompactFlowViewKeepsConditionalAndLongEdges(t *testing.T) {
	src := "```mermaid\nflowchart LR\n  a --> b --> c\n  a -->|YES| c\n```\n\n## a\ngo\n\n## b\ngo\n\n## c\ngo\n"
	w, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	view := w.CompactProgressView(nil, 100)
	if !strings.Contains(view, "c ← b, a [YES]") {
		t.Fatalf("long edge or condition disappeared from fallback:\n%s", view)
	}
	if strings.Contains(view, "○") {
		t.Fatalf("structural preview contains run status:\n%s", view)
	}
}

func TestInputNamesListsDistinctTemplateKeys(t *testing.T) {
	w, err := Parse("```mermaid\nflowchart LR\n  a --> b\n```\n\n## a\n{{input.topic}} {{input.style}}\n\n## b\n{{input.topic}} {{a}}\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(w.InputNames(), ","); got != "style,topic" {
		t.Fatalf("input names = %q", got)
	}
}

func TestPreviewStepsFollowGraphOrder(t *testing.T) {
	w, err := Parse("```mermaid\nflowchart LR\n  a --> b\n```\n\n## b\nSecond step\n\n## a\nFirst step\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := w.PreviewSteps(100); got != "  a — First step\n  b — Second step" {
		t.Fatalf("preview steps = %q", got)
	}
}

func TestPonytailAuditExampleHasParallelChecksAndJoin(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "flows", "ponytail-audit.md"))
	if err != nil {
		t.Fatal(err)
	}
	w, err := Parse(string(data))
	if err != nil {
		t.Fatal(err)
	}
	turns, _ := w.Bounds()
	if turns != 5 || len(w.Nodes) != 5 || w.MaxParallel != 3 {
		t.Fatalf("audit demo shape: turns=%d nodes=%d parallel=%d", turns, len(w.Nodes), w.MaxParallel)
	}
	for _, n := range w.Nodes {
		if n.Config["mode"] != "explore" {
			t.Errorf("%s is not read-only", n.ID)
		}
	}
	for _, id := range []string{"correctness", "security", "maintenance"} {
		n, ok := w.Node(id)
		if !ok || len(n.Upstream()) != 1 || n.Upstream()[0].From != "inspect" {
			t.Errorf("%s is not an independent check after inspect", id)
		}
	}
	if report, ok := w.Node("report"); !ok || len(report.Upstream()) != 3 {
		t.Fatal("report does not join all three checks")
	}
	runner := &stubRunner{outputs: map[string]string{
		"inspect":     "[REPOSITORY EVIDENCE]",
		"correctness": "[CORRECTNESS CANDIDATES]",
		"security":    "[SECURITY CANDIDATES]",
		"maintenance": "[MAINTENANCE CANDIDATES]",
		"report":      "Audit report",
	}}
	result := Run(t.Context(), w, runner, Options{Inputs: map[string]string{"scope": "internal/flow"}})
	if result.Failed(w) {
		t.Fatalf("audit example failed: %s", result.Summary(w))
	}
	if !strings.Contains(runner.prompts["inspect"], "internal/flow") {
		t.Fatal("inspection did not receive the requested scope")
	}
	for _, id := range []string{"correctness", "security", "maintenance", "report"} {
		if !strings.Contains(runner.prompts[id], runner.outputs["inspect"]) {
			t.Errorf("%s did not receive the repository evidence", id)
		}
	}
	for _, id := range []string{"correctness", "security", "maintenance"} {
		if !strings.Contains(runner.prompts["report"], runner.outputs[id]) {
			t.Errorf("report did not receive %s candidates", id)
		}
	}
}
