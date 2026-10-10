package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestCompactWorkflowViewDrawsBranchAndJoinWithoutRepeatedDetails(t *testing.T) {
	w, err := Parse("---\nname: audit\n---\n```mermaid\nflowchart LR\n  map --> bugs & risk & lean --> report\n```\n\n## map\nmode: explore\n\nMap\n\n## bugs\nmode: explore\n\nBugs\n\n## risk\nmode: explore\n\nRisk\n\n## lean\nmode: explore\n\nLean\n\n## report\nmode: explore\n\nReport\n")
	if err != nil {
		t.Fatal(err)
	}
	preview := w.CompactProgressView(nil, 100)
	if !strings.Contains(preview, "map ─┬──▶ bugs") || !strings.Contains(preview, "risk") || !strings.Contains(preview, "lean") || !strings.Contains(preview, "──▶ report") || strings.Contains(preview, "explore") || strings.Count(preview, "\n") != 3 {
		t.Fatalf("preview is not compact:\n%s", preview)
	}
	running := w.CompactProgressView(map[string]Status{"map": StatusSucceeded, "bugs": StatusRunning}, 100)
	if !strings.Contains(running, "map ✓ ─┬──▶ bugs ●") || !strings.Contains(running, "risk ○") || !strings.Contains(running, "report ○") {
		t.Fatalf("running topology:\n%s", running)
	}
}

func TestActivityStreamWrapsToTheAvailableWidth(t *testing.T) {
	w := &Workflow{}
	events := []ActivityEvent{{Node: "map", Text: "Bash(" + strings.Repeat("inspect/repository/", 15) + ")"}}
	for _, width := range []int{54, 100} {
		view := w.ActivityStreamView(events, width)
		if strings.Contains(view, "…") || !strings.Contains(view, "inspect/repository/") || strings.Count(view, "\n") < 2 {
			t.Fatalf("activity was clipped instead of wrapped at %d:\n%s", width, view)
		}
		for line := range strings.SplitSeq(view, "\n") {
			if ansi.StringWidth(line) > width {
				t.Fatalf("line exceeds %d columns: %q", width, line)
			}
		}
	}
}

func TestActivityStreamKeepsShortWordsTogether(t *testing.T) {
	w := &Workflow{}
	message := "I'll inspect the repository and report the first meaningful finding"
	view := w.ActivityStreamView([]ActivityEvent{{Node: "map", Text: message}}, 38)
	for _, word := range strings.Fields(message) {
		if !strings.Contains(view, word) {
			t.Fatalf("word %q was split across rows:\n%s", word, view)
		}
	}
}

func TestActivityStreamSeparatesToolCallFromResult(t *testing.T) {
	w := &Workflow{}
	events := []ActivityEvent{
		{Node: "map", ToolID: "one", Text: "Bash(head -120 internal/app/run.go)", ToolResult: "120 lines · 2.7 KiB · package app", ToolFinished: true},
		{Node: "map", ToolID: "two", Text: "Bash(cat missing.go)", ToolResult: "no such file", ToolFinished: true, ToolFailed: true},
		{Node: "map", ToolID: "three", Text: "Read(next.go)"},
	}
	for _, width := range []int{42, 100} {
		view := w.ActivityStreamView(events, width)
		for _, want := range []string{"✓ Bash(head -120", "└ 120 lines · 2.7 KiB", "✗ Bash(cat missing.go)", "└ no such file", "● Read(next.go)"} {
			if !strings.Contains(view, want) {
				t.Fatalf("tool activity missing %q at width %d:\n%s", want, width, view)
			}
		}
		if strings.Count(view, "Bash(cat missing.go)") != 1 {
			t.Fatalf("tool call repeated at width %d:\n%s", width, view)
		}
		for line := range strings.SplitSeq(view, "\n") {
			if ansi.StringWidth(line) > width {
				t.Fatalf("line exceeds %d columns: %q", width, line)
			}
		}
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

func TestCompactWorkflowViewKeepsConditionalAndLongEdges(t *testing.T) {
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

func TestActivityStreamGroupsConcurrentNodesAndWorkers(t *testing.T) {
	w := &Workflow{}
	view := w.ActivityStreamView([]ActivityEvent{
		{Node: "bugs", Text: "Read(first.go)"},
		{Node: "risk", Text: "Bash(check secrets)"},
		{Node: "bugs", Text: "› First finding"},
		{Node: "risk", Worker: "auth", Text: "Read(auth.go)"},
	}, 100)
	for _, want := range []string{"  Activity", "╭─ bugs", "╭─ risk", "╭─ risk·auth", "│  › First finding", "╰─"} {
		if !strings.Contains(view, want) {
			t.Errorf("grouped activity missing %q:\n%s", want, view)
		}
	}
	if strings.Count(view, "╭─ bugs") != 1 || strings.Contains(view, "\nNodes\n") {
		t.Fatalf("duplicate node groups:\n%s", view)
	}
}

func TestPonytailAuditExampleHasParallelChecksAndJoin(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "workflows", "ponytail-audit.md"))
	if err != nil {
		t.Fatal(err)
	}
	w, err := Parse(string(data))
	if err != nil {
		t.Fatal(err)
	}
	turns, _ := w.Bounds()
	if turns != 6 || len(w.Nodes) != 6 || w.MaxParallel != 3 {
		t.Fatalf("audit demo shape: turns=%d nodes=%d parallel=%d", turns, len(w.Nodes), w.MaxParallel)
	}
	for _, n := range w.Nodes {
		if n.Config["mode"] != "explore" {
			t.Errorf("%s is not read-only", n.ID)
		}
	}
	for _, id := range []string{"correctness", "safety", "maintainability"} {
		n, ok := w.Node(id)
		if !ok || len(n.Upstream()) != 1 || n.Upstream()[0].From != "understand_repo" {
			t.Errorf("%s is not an independent check after understand_repo", id)
		}
	}
	if verify, ok := w.Node("verify_findings"); !ok || len(verify.Upstream()) != 3 {
		t.Fatal("verification does not join all three checks")
	}
	if report, ok := w.Node("report"); !ok || len(report.Upstream()) != 1 || report.Upstream()[0].From != "verify_findings" {
		t.Fatal("report does not wait for verified findings")
	}
}
