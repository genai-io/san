package workflow

import (
	"context"
	"slices"
	"strings"
	"testing"
)

const optimizer = "```mermaid\nflowchart LR\n  spec --> draft --> review\n  review -->|FAIL x3| draft\n  review -->|PASS| ship\n```\n\n## spec\nWhat to build\n\n## draft\nWrite it. Previous review: {{review}}\n\nSpec: {{spec}}\n\n## review\nPASS or FAIL: {{draft}}\n\n## ship\nShip {{draft}}, reviewed as {{review}}\n"

func TestLoopUnrollsIntoAChain(t *testing.T) {
	w := mustParse(t, optimizer)

	var ids []string
	for _, n := range w.Nodes {
		ids = append(ids, n.ID)
	}
	want := []string{"spec", "draft#1", "draft#2", "draft#3", "review#1", "review#2", "review#3", "ship"}
	if !slices.Equal(ids, want) {
		t.Fatalf("nodes = %v,\nwant %v", ids, want)
	}
	if n, _ := w.Node("draft#2"); n.Base != "draft" {
		t.Fatalf("draft#2 base = %q", n.Base)
	}
	if len(w.Loops) != 1 || w.Loops[0].String() != "draft/review: up to 3 rounds" {
		t.Fatalf("loops = %+v", w.Loops)
	}
	if c := w.cycle(w.Edges); len(c) > 0 {
		t.Fatalf("unrolled graph still cycles through %v", c)
	}

	// Every round can escape to ship; only the rounds before the last feed
	// the next draft.
	var toShip, toNextDraft int
	for _, e := range w.Edges {
		switch {
		case e.To == "ship":
			toShip++
			if e.Label != "PASS" {
				t.Fatalf("escape edge %+v lost its label", e)
			}
		case strings.HasPrefix(e.From, "review#") && strings.HasPrefix(e.To, "draft#"):
			toNextDraft++
		}
	}
	if toShip != 3 || toNextDraft != 2 {
		t.Fatalf("escapes = %d, retries = %d; want 3 and 2", toShip, toNextDraft)
	}
}

func TestLoopSecondRoundReadsTheFirst(t *testing.T) {
	w := mustParse(t, optimizer)
	r := &stubRunner{outputs: map[string]string{
		"spec":     "S",
		"draft#1":  "D1",
		"review#1": "FAIL",
		"draft#2":  "D2",
		"review#2": "PASS",
		"ship":     "shipped",
	}}
	res := Run(context.Background(), w, r, Options{})

	if res.Failed(w) {
		t.Fatalf("failed: %s", res.Summary(w))
	}
	if got := r.prompts["draft#1"]; got != "Write it. Previous review: \n\nSpec: S" {
		t.Fatalf("draft#1 prompt = %q, want an empty {{review}} on the first round", got)
	}
	if got := r.prompts["draft#2"]; got != "Write it. Previous review: FAIL\n\nSpec: S" {
		t.Fatalf("draft#2 prompt = %q, want the previous round's review", got)
	}
	if got := r.prompts["review#2"]; got != "PASS or FAIL: D2" {
		t.Fatalf("review#2 prompt = %q, want its own round's draft", got)
	}
	if got := r.prompts["ship"]; got != "Ship D2, reviewed as PASS" {
		t.Fatalf("ship prompt = %q, want the round that actually passed", got)
	}
	if res.Nodes["draft#3"].Status != StatusOmitted || res.Nodes["review#3"].Status != StatusOmitted {
		t.Fatalf("round 3 = %s/%s, want omitted", res.Nodes["draft#3"].Status, res.Nodes["review#3"].Status)
	}
}

func TestLoopFirstRoundPassingSkipsTheRest(t *testing.T) {
	w := mustParse(t, optimizer)
	r := &stubRunner{outputs: map[string]string{"spec": "S", "draft#1": "D1", "review#1": "PASS", "ship": "ok"}}
	res := Run(context.Background(), w, r, Options{})

	if res.Failed(w) || res.Nodes["ship"].Status != StatusSucceeded {
		t.Fatalf("ship = %s: %s", res.Nodes["ship"].Status, res.Summary(w))
	}
	if r.prompts["ship"] != "Ship D1, reviewed as PASS" {
		t.Fatalf("ship prompt = %q", r.prompts["ship"])
	}
	for _, id := range []string{"draft#2", "review#2", "draft#3", "review#3"} {
		if res.Nodes[id].Status != StatusOmitted {
			t.Fatalf("%s = %s, want omitted", id, res.Nodes[id].Status)
		}
	}
}

func TestLoopExhaustionFailsTheWorkflow(t *testing.T) {
	w := mustParse(t, optimizer)
	r := &stubRunner{outputs: map[string]string{
		"spec": "S", "draft#1": "D1", "review#1": "FAIL",
		"draft#2": "D2", "review#2": "FAIL",
		"draft#3": "D3", "review#3": "FAIL",
	}}
	res := Run(context.Background(), w, r, Options{})

	if !res.Failed(w) {
		t.Fatalf("exhausting the rounds must fail the workflow:\n%s", res.Summary(w))
	}
	err := res.Nodes["review#3"].Err
	if err == nil || !strings.Contains(err.Error(), `returned "FAIL"; its edges want PASS`) {
		t.Fatalf("review#3 err = %v", err)
	}
	if res.Nodes["ship"].Status != StatusSkipped {
		t.Fatalf("ship = %s, want skipped — a failed review must not flow on", res.Nodes["ship"].Status)
	}
	// Only the last round can exhaust; the earlier ones took the retry.
	for _, id := range []string{"review#1", "review#2"} {
		if res.Nodes[id].Status != StatusSucceeded {
			t.Fatalf("%s = %s, want succeeded", id, res.Nodes[id].Status)
		}
	}
}

func TestLoopBodyOfSeveralNodes(t *testing.T) {
	src := "```mermaid\nflowchart LR\n  spec --> draft --> test --> review\n  review -->|FAIL x2| draft\n  review -->|PASS| ship\n```\n\n## spec\ns\n\n## draft\nd {{review}}\n\n## test\nt {{draft}}\n\n## review\nr {{test}}\n\n## ship\nship {{draft}}\n"
	w := mustParse(t, src)
	if len(w.Nodes) != 8 {
		t.Fatalf("nodes = %d, want spec + 3×2 + ship", len(w.Nodes))
	}
	r := &stubRunner{outputs: map[string]string{
		"spec": "S", "draft#1": "D1", "test#1": "T1", "review#1": "FAIL",
		"draft#2": "D2", "test#2": "T2", "review#2": "PASS", "ship": "ok",
	}}
	res := Run(context.Background(), w, r, Options{})
	if res.Failed(w) {
		t.Fatalf("failed: %s", res.Summary(w))
	}
	if r.prompts["test#2"] != "t D2" || r.prompts["ship"] != "ship D2" {
		t.Fatalf("prompts = %q / %q", r.prompts["test#2"], r.prompts["ship"])
	}
}

func TestLoopValidation(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"unbounded back edge": {
			"```mermaid\nflowchart LR\n  a --> b\n  b -->|FAIL| a\n```\n## a\nx\n## b\ny\n",
			"a back edge needs a bound, as in `b -->|FAIL x3| a`",
		},
		"zero rounds": {
			strings.Replace(optimizer, "FAIL x3", "FAIL x0", 1),
			"must be between 1 and 10 rounds",
		},
		"not an ancestor": {
			"```mermaid\nflowchart LR\n  a --> b\n  a -->|FAIL x2| c\n  c --> d\n```\n## a\nx\n## b\ny\n## c\nz\n## d\nw\n",
			"c is not an ancestor of a, so there is no loop to bound",
		},
		"for_each in the body": {
			"```mermaid\nflowchart LR\n  plan --> work --> check\n  check -->|FAIL x2| work\n  check -->|PASS| done\n```\n## plan\np\n## work\nfor_each: plan\nmax_workers: 3\n\n{{item}}\n## check\nc {{work}}\n## done\nd\n",
			"has for_each inside the loop work/check: up to 2 rounds",
		},
		"outside edge into the body": {
			"```mermaid\nflowchart LR\n  spec --> draft --> review\n  extra --> review\n  review -->|FAIL x2| draft\n  review -->|PASS| ship\n```\n## spec\ns\n## draft\nd\n## review\nr {{draft}}\n## extra\ne\n## ship\nsh\n",
			"extra --> review enters the body from outside; route it through draft",
		},
		"nested loops": {
			"```mermaid\nflowchart LR\n  a --> b --> c --> d\n  c -->|R x2| b\n  d -->|R x2| a\n  d -->|PASS| e\n```\n## a\nx\n## b\ny\n## c\nz\n## d\nw\n## e\nv\n",
			"is inside two loops",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(c.src); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v,\nwant it to mention %q", err, c.want)
			}
		})
	}
}

func TestLoopBoundsCountEveryRound(t *testing.T) {
	w := mustParse(t, optimizer)
	turns, fanOut := w.Bounds()
	if turns != 8 {
		t.Fatalf("turns = %d, want spec + 3 drafts + 3 reviews + ship", turns)
	}
	if !slices.Contains(fanOut, "draft/review: up to 3 rounds") {
		t.Fatalf("fanOut = %v", fanOut)
	}
}

func TestLoopOffScriptAnswerFailsTheRoundItCameFrom(t *testing.T) {
	// Round 1 answers neither FAIL nor PASS. Before, both of its ways out went
	// untaken, everything after it was omitted, and the run reported success
	// having shipped nothing.
	w := mustParse(t, optimizer)
	r := &stubRunner{outputs: map[string]string{"spec": "S", "draft#1": "D1", "review#1": "looks fine to me"}}
	res := Run(context.Background(), w, r, Options{})

	err := res.Nodes["review#1"].Err
	if err == nil || !strings.Contains(err.Error(), `returned "looks fine to me"; its edges want PASS or FAIL`) {
		t.Fatalf("review#1 err = %v, want it to name the answers the round accepts", err)
	}
	if !res.Failed(w) {
		t.Fatalf("an off-script round must fail the run:\n%s", res.Summary(w))
	}
}

func TestGateStillStopsQuietlyOnAnUnmatchedAnswer(t *testing.T) {
	// The loop rule must not leak into plain nodes: a gate that answers NO has
	// no edge for it on purpose, and that is a successful "stop here".
	src := "```mermaid\nflowchart LR\n  triage -->|YES| deep --> report\n```\n\n## triage\nt\n\n## deep\nd\n\n## report\nr\n"
	w := mustParse(t, src)
	res := Run(context.Background(), w, &stubRunner{outputs: map[string]string{"triage": "NO"}}, Options{})

	if res.Failed(w) || res.Nodes["triage"].Status != StatusSucceeded {
		t.Fatalf("a gate's NO is not a failure:\n%s", res.Summary(w))
	}
	if res.Nodes["deep"].Status != StatusOmitted {
		t.Fatalf("deep = %s, want omitted", res.Nodes["deep"].Status)
	}
}

func TestLoopBoundIsCappedBeforeUnrolling(t *testing.T) {
	// Parsing runs while the approval dialog is built, so a bound this large
	// must be refused before any round is allocated.
	for _, bound := range []string{"x11", "x100000000", "x99999999999999999999999"} {
		src := strings.Replace(optimizer, "FAIL x3", "FAIL "+bound, 1)
		if _, err := Parse(src); err == nil || !strings.Contains(err.Error(), "must be between 1 and 10 rounds") {
			t.Errorf("%s: err = %v, want the cap", bound, err)
		}
	}
	if _, err := Parse(strings.Replace(optimizer, "FAIL x3", "FAIL x10", 1)); err != nil {
		t.Fatalf("x10 is within the cap: %v", err)
	}
}

func TestSecondLoopAlwaysStartsAtItsFirstRound(t *testing.T) {
	// Two loops in sequence. However many rounds the first takes, control
	// must enter the second at d#1 — not at the round number the first loop
	// happened to finish on.
	src := "```mermaid\nflowchart LR\n  a --> b --> c\n  c -->|R x2| b\n  c -->|OK| d --> e\n  e -->|R x2| d\n  e -->|OK| f\n```\n\n## a\na\n## b\nb\n## c\nc\n## d\nd\n## e\ne\n## f\nf\n"
	w := mustParse(t, src)

	// The first loop passes on its second round; the second still runs d#1.
	r := &stubRunner{outputs: map[string]string{
		"a": "A", "b#1": "B1", "c#1": "R", "b#2": "B2", "c#2": "OK",
		"d#1": "D1", "e#1": "OK", "f": "F",
	}}
	res := Run(context.Background(), w, r, Options{})
	if res.Failed(w) {
		t.Fatalf("failed:\n%s", res.Summary(w))
	}
	if res.Nodes["d#1"].Status != StatusSucceeded || res.Nodes["f"].Status != StatusSucceeded {
		t.Fatalf("d#1 = %s, f = %s; want both to run", res.Nodes["d#1"].Status, res.Nodes["f"].Status)
	}
}

func TestGateInsideALoopStillStopsQuietly(t *testing.T) {
	// Only the node carrying the back edge declared its full set of answers.
	// A gate elsewhere in the body keeps its gate semantics: NO is a stop.
	src := "```mermaid\nflowchart LR\n  draft --> triage\n  triage -->|YES| review\n  review -->|FAIL x2| draft\n  review -->|PASS| ship\n```\n\n## draft\nd\n\n## triage\nt\n\n## review\nr\n\n## ship\ns\n"
	w := mustParse(t, src)
	res := Run(context.Background(), w, &stubRunner{outputs: map[string]string{"draft#1": "D", "triage#1": "NO"}}, Options{})

	if res.Nodes["triage#1"].Status != StatusSucceeded {
		t.Fatalf("triage#1 = %s (%v); a gate's NO inside a loop is still a stop", res.Nodes["triage#1"].Status, res.Nodes["triage#1"].Err)
	}
	if res.Failed(w) {
		t.Fatalf("a gate stopping is not a failure:\n%s", res.Summary(w))
	}
}
