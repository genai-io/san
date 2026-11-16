package flow

import (
	"context"
	"strings"
	"testing"
)

func TestFinalOutputSuccessfulSinks(t *testing.T) {
	w := mustParse(t, "```mermaid\nflowchart LR\n source --> z & a & failed & skipped & omitted & empty & missing\n```\n## source\nsource\n## z\nz\n## a\na\n## failed\nfailed\n## skipped\nskipped\n## omitted\nomitted\n## empty\nempty\n## missing\nmissing\n")
	res := &Result{Nodes: map[string]*NodeResult{
		"source":  {Status: StatusSucceeded, Output: "intermediate"},
		"z":       {Status: StatusSucceeded, Output: " \n# Conclusion\n\nFull answer\n "},
		"a":       {Status: StatusFailed, Output: "partial"},
		"failed":  {Status: StatusFailed, Output: "failed text"},
		"skipped": {Status: StatusSkipped, Output: "skipped text"},
		"omitted": {Status: StatusOmitted, Output: "omitted text"},
		"empty":   {Status: StatusSucceeded, Output: " \n\t"},
	}}
	const answer = "# Conclusion\n\nFull answer"
	if got := res.FinalOutput(w); got != answer {
		t.Fatalf("single answer = %q", got)
	}
	res.Nodes["a"] = &NodeResult{Status: StatusSucceeded, Output: " second answer \n"}
	if got, want := res.FinalOutput(w), "## z\n"+answer+"\n\n## a\nsecond answer"; got != want {
		t.Fatalf("multiple answers = %q, want %q", got, want)
	}
	res.Nodes["z"].Status = StatusFailed
	res.Nodes["a"].Status = StatusOmitted
	if got := res.FinalOutput(w); got != "" {
		t.Fatalf("no successful sink = %q", got)
	}
}

func TestFinalOutputConditionalSink(t *testing.T) {
	w := mustParse(t, "```mermaid\nflowchart LR\n gate -->|YES| selected\n gate -->|NO| other\n```\n## gate\nroute\n## selected\nselected\n## other\nother\n")
	res := Run(context.Background(), w, &stubRunner{outputs: map[string]string{"gate": "YES", "selected": " chosen conclusion ", "other": "not selected"}}, Options{})
	if got := res.FinalOutput(w); got != "chosen conclusion" {
		t.Fatalf("conditional answer = %q (%s)", got, res.Summary(w))
	}
	if summary := res.Summary(w); !strings.Contains(summary, "## selected\nchosen conclusion") || !strings.Contains(summary, "⊘ other") {
		t.Fatalf("summary changed: %s", summary)
	}
}
