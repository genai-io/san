package workflow

import (
	"strings"
	"testing"
)

const releaseCheck = `---
name: release-check
max_parallel: 3
---

Routing nested in sectioning.

` + "```mermaid" + `
flowchart LR
  diff --> triage & perf & tests
  triage -->|HIGH| sec & threat
  triage -->|LOW| quick
  sec & threat & quick & perf & tests --> report
` + "```" + `

## diff
agent: Explore

Summarize {{input.base}}..HEAD

## triage
mode: explore

HIGH or LOW: {{diff}}

## sec
mode: explore

Security: {{diff}}

## threat
mode: explore

Threats: {{diff}}

## quick
mode: explore

Quick: {{diff}}

## perf
mode: explore

Perf: {{diff}}

## tests
Run make test

## report
model: opus

Merge: {{sec}} {{threat}} {{quick}} {{perf}} {{tests}}
`

func TestParseReleaseCheck(t *testing.T) {
	w, err := Parse(releaseCheck)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if w.Name != "release-check" || w.MaxParallel != 3 {
		t.Fatalf("frontmatter: name=%q max_parallel=%d", w.Name, w.MaxParallel)
	}
	if len(w.Nodes) != 8 || len(w.Edges) != 11 {
		t.Fatalf("nodes=%d edges=%d, want 8 and 11", len(w.Nodes), len(w.Edges))
	}
	diff, _ := w.Node("diff")
	if diff.Config["agent"] != "Explore" || diff.Prompt != "Summarize {{input.base}}..HEAD" {
		t.Fatalf("diff = %+v", diff)
	}
	sec, _ := w.Node("sec")
	if up := sec.Upstream(); len(up) != 1 || up[0].From != "triage" || up[0].Label != "HIGH" {
		t.Fatalf("sec upstream = %+v", up)
	}
	report, _ := w.Node("report")
	if len(report.Upstream()) != 5 || report.Config["model"] != "opus" {
		t.Fatalf("report = %+v", report)
	}
}

func TestParseTemplatesSeeAncestorsNotJustParents(t *testing.T) {
	w, err := Parse("```mermaid\nflowchart LR\n  diff --> triage -->|HIGH| sec\n```\n\n## diff\nd\n\n## triage\n{{diff}}\n\n## sec\n{{diff}} {{triage}}\n")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	sec, _ := w.Node("sec")
	if len(sec.Upstream()) != 1 {
		t.Fatalf("sec upstream = %+v", sec.Upstream())
	}
}

func TestParseMinimalDefaults(t *testing.T) {
	w, err := Parse("```mermaid\nflowchart LR\n  only\n```\n\n## only\ngo\n")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if w.MaxParallel != defaultMaxParallel || len(w.Nodes) != 1 || len(w.Edges) != 0 {
		t.Fatalf("w = %+v", w)
	}
}

func TestParseFenceInsidePromptIsNotAHeading(t *testing.T) {
	src := "```mermaid\nflowchart LR\n  a --> b\n```\n\n## a\nwrite this file:\n```md\n## not a node\n```\n\n## b\n{{a}}\n"
	w, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	a, _ := w.Node("a")
	if !strings.Contains(a.Prompt, "## not a node") {
		t.Fatalf("a prompt lost its fenced heading: %q", a.Prompt)
	}
}

func TestParseRejects(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"no graph":      {"## a\nx\n", "no ```mermaid block"},
		"no header":     {"```mermaid\n  a --> b\n```\n## a\nx\n## b\ny\n", "must start with `flowchart LR`"},
		"shape syntax":  {"```mermaid\nflowchart LR\n  a[Start] --> b\n```\n## a\nx\n## b\ny\n", "unsupported mermaid syntax"},
		"empty label":   {"```mermaid\nflowchart LR\n  a -->|| b\n```\n## a\nx\n## b\ny\n", "empty edge label"},
		"missing node":  {"```mermaid\nflowchart LR\n  a --> b\n```\n## a\nx\n", "no `## b` section"},
		"extra section": {"```mermaid\nflowchart LR\n  a\n```\n## a\nx\n## b\ny\n", "`## b` is not in the graph"},
		"cycle":         {"```mermaid\nflowchart LR\n  a --> b --> a\n```\n## a\nx\n## b\ny\n", "cycle through a, b"},
		"unconnected":   {"```mermaid\nflowchart LR\n  a --> b\n  c\n```\n## a\nx\n## b\n{{c}}\n## c\nz\n", "no path c --> … --> b"},
		"bad key":       {"```mermaid\nflowchart LR\n  a\n```\n## a\nmodle: x\n\nprompt\n", "unknown config key \"modle\""},
		"bad mode":      {"```mermaid\nflowchart LR\n  a\n```\n## a\nmode: bypass\n\nprompt\n", "mode must be explore, edit, or default"},
		"empty prompt":  {"```mermaid\nflowchart LR\n  a\n```\n## a\nmode: explore\n", "has no prompt"},
		"dup edge":      {"```mermaid\nflowchart LR\n  a --> b\n  a -->|X| b\n```\n## a\nx\n## b\ny\n", "appears twice"},
		"dup section":   {"```mermaid\nflowchart LR\n  a\n```\n## a\nx\n## a\ny\n", "defined twice"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(c.src)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to mention %q", err, c.want)
			}
		})
	}
}

func TestParseReportsAllProblemsAtOnce(t *testing.T) {
	_, err := Parse("```mermaid\nflowchart LR\n  a --> b\n```\n## a\n\n## c\ny\n")
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"no `## b` section", "`## c` is not in the graph", "node a has no prompt"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
}
