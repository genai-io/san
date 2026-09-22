package workflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const orchestrator = "```mermaid\nflowchart LR\n  plan --> review --> merge\n```\n\n## plan\nSplit the work, JSON\n\n## review\nfor_each: plan.tasks\nmax_workers: 3\nmode: explore\n\nReview {{item.prompt}} in {{item.pkg}}\n\n## merge\nMerge:\n{{review}}\n"

const planJSON = `Here is the plan:

` + "```json" + `
{"tasks":[
  {"name":"llm","pkg":"internal/llm","prompt":"error handling"},
  {"name":"tool","pkg":"internal/tool","prompt":"permissions"}
]}
` + "```" + `

Two tasks should cover it.`

func TestForEachFansOutOverThePlan(t *testing.T) {
	w := mustParse(t, orchestrator)
	r := &stubRunner{outputs: map[string]string{
		"plan":        planJSON,
		"review/llm":  "llm findings",
		"review/tool": "tool findings",
		"merge":       "merged",
	}}
	res := Run(context.Background(), w, r, Options{})

	if res.Failed(w) {
		t.Fatalf("workflow failed: %s", res.Summary(w))
	}
	if r.prompts["review/llm"] != "Review error handling in internal/llm" {
		t.Fatalf("llm worker prompt = %q", r.prompts["review/llm"])
	}
	if r.prompts["review/tool"] != "Review permissions in internal/tool" {
		t.Fatalf("tool worker prompt = %q", r.prompts["review/tool"])
	}
	if want := "## llm\nllm findings\n\n## tool\ntool findings"; res.Nodes["review"].Output != want {
		t.Fatalf("review output = %q", res.Nodes["review"].Output)
	}
	if !strings.Contains(r.prompts["merge"], "## llm\nllm findings") {
		t.Fatalf("merge prompt = %q", r.prompts["merge"])
	}
}

func TestForEachRefusesAPlanLargerThanMaxWorkers(t *testing.T) {
	// max_workers bounds how many workers a plan may ask for, not how many
	// run at once — a plan over the bound is refused outright, which is what
	// makes the worst case knowable before launch. Concurrency is
	// max_parallel's job.
	over := strings.Replace(orchestrator, "max_workers: 3", "max_workers: 1", 1)
	w := mustParse(t, over)
	res := Run(context.Background(), w, &stubRunner{outputs: map[string]string{"plan": planJSON}}, Options{})

	if err := res.Nodes["review"].Err; err == nil || !strings.Contains(err.Error(), "2 items but max_workers is 1") {
		t.Fatalf("oversized plan err = %v, want a refusal naming the bound", err)
	}
	if res.Nodes["merge"].Status != StatusSkipped {
		t.Fatalf("merge = %s, want skipped", res.Nodes["merge"].Status)
	}
}

func TestForEachWorkersShareTheRunsSlots(t *testing.T) {
	// Three workers under max_parallel: 2 — the run's bound is what holds
	// them back, since the plan is already within max_workers.
	src := strings.Replace(orchestrator, "```mermaid", "---\nmax_parallel: 2\n---\n\n```mermaid", 1)
	w := mustParse(t, src)
	release := make(chan struct{})
	r := &stubRunner{
		outputs: map[string]string{"plan": `{"tasks":[{"name":"a"},{"name":"b"},{"name":"c"}]}`},
		block:   map[string]chan struct{}{"review/a": release, "review/b": release, "review/c": release},
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(release)
	}()
	Run(context.Background(), w, r, Options{})

	if r.peak.Load() != 2 {
		t.Fatalf("peak = %d, want max_parallel", r.peak.Load())
	}
}

func TestForEachOverStringItems(t *testing.T) {
	src := "```mermaid\nflowchart LR\n  plan --> check\n```\n\n## plan\nlist files\n\n## check\nfor_each: plan\nmax_workers: 4\n\nCheck {{item}}\n"
	w := mustParse(t, src)
	r := &stubRunner{outputs: map[string]string{"plan": `["a.go", "b.go"]`}}
	res := Run(context.Background(), w, r, Options{})

	if res.Failed(w) {
		t.Fatalf("failed: %s", res.Summary(w))
	}
	if r.prompts["check/#1"] != "Check a.go" || r.prompts["check/#2"] != "Check b.go" {
		t.Fatalf("prompts = %+v", r.prompts)
	}
}

func TestForEachPlanProblemsFailTheNode(t *testing.T) {
	cases := map[string]struct{ output, want string }{
		"no json":   {"I could not split this up", "no JSON plan"},
		"not array": {`{"tasks":"everything"}`, "not a JSON array"},
		"no field":  {`{"items":[1]}`, `no "tasks" field`},
		"empty":     {`{"tasks":[]}`, "plan is empty"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			w := mustParse(t, orchestrator)
			res := Run(context.Background(), w, &stubRunner{outputs: map[string]string{"plan": c.output}}, Options{})
			if err := res.Nodes["review"].Err; err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to mention %q", err, c.want)
			}
		})
	}
}

func TestForEachValidation(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"unbounded":     {strings.Replace(orchestrator, "max_workers: 3\n", "", 1), "for_each requires max_workers"},
		"stray workers": {strings.Replace(orchestrator, "for_each: plan.tasks\n", "", 1), "max_workers means nothing without for_each"},
		"bad source":    {strings.Replace(orchestrator, "for_each: plan.tasks", "for_each: merge.tasks", 1), "for_each reads merge but the graph has no path"},
		"bad shape":     {strings.Replace(orchestrator, "for_each: plan.tasks", "for_each: plan.a.b", 1), "for_each must be `node` or `node.field`"},
		"zero workers":  {strings.Replace(orchestrator, "max_workers: 3", "max_workers: 0", 1), "max_workers must be a positive number"},
		"stray item":    {strings.Replace(orchestrator, "Merge:\n{{review}}", "Merge {{item}}", 1), "node merge references {{item}} but has no for_each"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(c.src); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to mention %q", err, c.want)
			}
		})
	}
}

func TestBoundsCountTheWorstCase(t *testing.T) {
	w := mustParse(t, orchestrator)
	turns, fanOut := w.Bounds()
	if turns != 5 {
		t.Fatalf("turns = %d, want plan + 3 workers + merge", turns)
	}
	if len(fanOut) != 1 || fanOut[0] != "review: up to 3 workers over plan.tasks" {
		t.Fatalf("fanOut = %+v", fanOut)
	}
}

func TestLoadAndFind(t *testing.T) {
	project, user := t.TempDir(), t.TempDir()
	write := func(dir, file, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(project, "review.md", "---\nname: review\ndescription: project copy\n---\n```mermaid\nflowchart LR\n  a\n```\n\n## a\nhi\n")
	write(user, "review.md", "---\nname: review\ndescription: user copy\n---\n```mermaid\nflowchart LR\n  a\n```\n\n## a\nhi\n")
	write(user, "release.md", "```mermaid\nflowchart LR\n  a\n```\n\n## a\nship\n")
	write(user, "broken.md", "---\nname: broken\n---\nno graph here\n")
	write(user, "notes.txt", "ignored")

	got := Load(project, user)
	var names []string
	for _, s := range got {
		names = append(names, s.Name)
	}
	if want := []string{"broken", "release", "review"}; !slices.Equal(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	if got[2].Description != "project copy" {
		t.Fatalf("review description = %q, want the project copy to win", got[2].Description)
	}

	w, err := Find("release", project, user)
	if err != nil || w.Name != "release" {
		t.Fatalf("Find(release) = %+v, %v; want the filename as its name", w, err)
	}
	if _, err := Find("broken", project, user); err == nil || !strings.Contains(err.Error(), "no ```mermaid block") {
		t.Fatalf("Find(broken) err = %v, want the parse error", err)
	}
	if _, err := Find("nope", project, user); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Find(nope) err = %v, want ErrNotFound", err)
	}
}

func TestForEachToleratedFailurePassesOnTheFinishedWorkers(t *testing.T) {
	src := strings.Replace(orchestrator, "## review\n", "## review\ncontinue_on_error: true\n", 1)
	w := mustParse(t, src)
	r := &stubRunner{
		outputs: map[string]string{"plan": planJSON, "review/llm": "llm findings"},
		fails:   map[string]bool{"review/tool": true},
	}
	res := Run(context.Background(), w, r, Options{})

	if res.Nodes["review"].Status != StatusFailed {
		t.Fatalf("review = %s, want failed — one worker did fail", res.Nodes["review"].Status)
	}
	if res.Failed(w) {
		t.Fatal("continue_on_error should keep the workflow's verdict clean")
	}
	// The worker that finished did whole work; discarding it would throw away
	// what the fan-out was paid for.
	if got := r.prompts["merge"]; got != "Merge:\n## llm\nllm findings" {
		t.Fatalf("merge prompt = %q, want the finished worker and no gap for the failed one", got)
	}
}

func TestPlainToleratedFailurePassesNothingOn(t *testing.T) {
	src := "```mermaid\nflowchart LR\n  a --> b\n```\n\n## a\ncontinue_on_error: true\n\nhalf\n\n## b\ngot [{{a}}]\n"
	w := mustParse(t, src)
	r := &stubRunner{fails: map[string]bool{"a": true}}
	Run(context.Background(), w, r, Options{})

	if got := r.prompts["b"]; got != "got []" {
		t.Fatalf("b prompt = %q; a half-finished turn must not read as content", got)
	}
}

func TestPlanKeepsNumbersAsWritten(t *testing.T) {
	src := "```mermaid\nflowchart LR\n  plan --> work\n```\n\n## plan\np\n\n## work\nfor_each: plan.tasks\nmax_workers: 2\n\nissue {{item.id}} of {{item.big}} — {{item}}\n"
	w := mustParse(t, src)
	r := &stubRunner{outputs: map[string]string{
		"plan": `{"tasks":[{"name":"one","id":1000000,"big":12345678901234567890}]}`,
	}}
	Run(context.Background(), w, r, Options{})

	got := r.prompts["work/one"]
	// A float64 round trip would render these as 1e+06 and 1.2345678901234567e+19,
	// and would have rounded the large one before the worker ever saw it.
	if !strings.Contains(got, "issue 1000000 of 12345678901234567890") {
		t.Fatalf("worker prompt = %q; the plan's numbers must reach it as written", got)
	}
	if !strings.Contains(got, `"big":12345678901234567890`) {
		t.Fatalf("worker prompt = %q; {{item}} must carry the number unrounded", got)
	}
}
