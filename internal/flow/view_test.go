package flow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

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
	if !strings.Contains(running, "map ● ─┬──▶ bugs ◉") || !strings.Contains(running, "risk ○") || !strings.Contains(running, "report ○") {
		t.Fatalf("running topology:\n%s", running)
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
