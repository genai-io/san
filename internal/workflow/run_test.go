package workflow

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// stubRunner answers each node from a table and records what it was asked.
type stubRunner struct {
	mu      sync.Mutex
	outputs map[string]string // node id -> output
	fails   map[string]bool
	block   map[string]chan struct{} // node id -> release channel
	prompts map[string]string
	calls   []string
	running atomic.Int32
	peak    atomic.Int32
}

func (s *stubRunner) RunNode(ctx context.Context, c Call) (string, error) {
	key := c.Node.ID
	if c.Label != "" {
		key += "/" + c.Label
	}
	s.mu.Lock()
	if s.prompts == nil {
		s.prompts = map[string]string{}
	}
	s.prompts[key] = c.Prompt
	s.calls = append(s.calls, key)
	s.mu.Unlock()

	cur := s.running.Add(1)
	defer s.running.Add(-1)
	for {
		old := s.peak.Load()
		if cur <= old || s.peak.CompareAndSwap(old, cur) {
			break
		}
	}
	if ch, ok := s.block[key]; ok {
		select {
		case <-ch:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if s.fails[key] {
		return "", fmt.Errorf("%s exploded", key)
	}
	return s.outputs[key], nil
}

func mustParse(t *testing.T, src string) *Workflow {
	t.Helper()
	w, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return w
}

const chain = "```mermaid\nflowchart LR\n  a --> b --> c\n```\n\n## a\nstart\n\n## b\ngot {{a}}\n\n## c\ngot {{b}} and {{input.base}}\n"

func TestRunSequenceRendersUpstreamOutput(t *testing.T) {
	w := mustParse(t, chain)
	r := &stubRunner{outputs: map[string]string{"a": "A", "b": "B", "c": "C"}}
	res := Run(context.Background(), w, r, Options{Inputs: map[string]string{"base": "main"}})

	if res.Failed(w) {
		t.Fatalf("workflow failed: %s", res.Summary(w))
	}
	if r.prompts["b"] != "got A" || r.prompts["c"] != "got B and main" {
		t.Fatalf("prompts = %+v", r.prompts)
	}
	if r.peak.Load() != 1 {
		t.Fatalf("a chain ran %d nodes at once", r.peak.Load())
	}
	if !strings.Contains(res.Summary(w), "## c\nC") {
		t.Fatalf("summary should carry the sink output:\n%s", res.Summary(w))
	}
}

func TestRunFanOutHonoursMaxParallel(t *testing.T) {
	src := "---\nmax_parallel: 2\n---\n```mermaid\nflowchart LR\n  src --> x & y & z --> sink\n```\n\n## src\ns\n\n## x\n{{src}}\n\n## y\n{{src}}\n\n## z\n{{src}}\n\n## sink\n{{x}}{{y}}{{z}}\n"
	w := mustParse(t, src)
	release := make(chan struct{})
	r := &stubRunner{
		outputs: map[string]string{"x": "1", "y": "2", "z": "3"},
		block:   map[string]chan struct{}{"x": release, "y": release, "z": release},
	}
	go func() {
		// Let the fan-out pile up against the cap, then let it through.
		time.Sleep(50 * time.Millisecond)
		close(release)
	}()
	res := Run(context.Background(), w, r, Options{})

	if res.Failed(w) {
		t.Fatalf("workflow failed: %s", res.Summary(w))
	}
	if r.peak.Load() != 2 {
		t.Fatalf("peak concurrency = %d, want 2", r.peak.Load())
	}
	if r.prompts["sink"] != "123" {
		t.Fatalf("sink prompt = %q", r.prompts["sink"])
	}
}

func TestRunConditionalEdgesOmitTheBranchNotTaken(t *testing.T) {
	src := "```mermaid\nflowchart LR\n  triage -->|HIGH| deep\n  triage -->|LOW| quick\n  deep & quick --> report\n```\n\n## triage\nhow bad\n\n## deep\n{{triage}}\n\n## quick\n{{triage}}\n\n## report\nD=[{{deep}}] Q=[{{quick}}]\n"
	w := mustParse(t, src)
	r := &stubRunner{outputs: map[string]string{"triage": "HIGH\n", "deep": "d"}}
	res := Run(context.Background(), w, r, Options{})

	if got := res.Nodes["quick"].Status; got != StatusOmitted {
		t.Fatalf("quick = %s, want omitted", got)
	}
	// Omitted means never run: the untaken branch costs no turn at all.
	if slices.Contains(r.calls, "quick") {
		t.Fatalf("calls = %v; an omitted node must never reach the runner", r.calls)
	}
	if got := res.Nodes["deep"].Status; got != StatusSucceeded {
		t.Fatalf("deep = %s, want succeeded", got)
	}
	if r.prompts["report"] != "D=[d] Q=[]" {
		t.Fatalf("report prompt = %q", r.prompts["report"])
	}
	if res.Failed(w) {
		t.Fatal("an omitted branch is not a failure")
	}
}

func TestRunScopeFollowsTakenPathsOnly(t *testing.T) {
	// c sees a through the taken edge a --> b; d sees a only if a said X.
	src := "```mermaid\nflowchart LR\n  a --> b --> c\n  a -->|X| d\n  z --> d\n```\n\n## a\na\n\n## b\n{{a}}\n\n## c\n[{{a}}]\n\n## d\n[{{a}}] [{{z}}]\n\n## z\nz\n"
	w := mustParse(t, src)
	r := &stubRunner{outputs: map[string]string{"a": "Y", "b": "B", "z": "Z"}}
	Run(context.Background(), w, r, Options{})

	if r.prompts["c"] != "[Y]" {
		t.Fatalf("c prompt = %q, want the grandparent's output", r.prompts["c"])
	}
	if r.prompts["d"] != "[] [Z]" {
		t.Fatalf("d prompt = %q, want a hidden behind its untaken edge", r.prompts["d"])
	}
}

func TestRunAllUpstreamsOmittedOmitsDownstream(t *testing.T) {
	src := "```mermaid\nflowchart LR\n  gate -->|YES| work --> done\n```\n\n## gate\ng\n\n## work\n{{gate}}\n\n## done\n{{work}}\n"
	w := mustParse(t, src)
	res := Run(context.Background(), w, &stubRunner{outputs: map[string]string{"gate": "NO"}}, Options{})

	for _, id := range []string{"work", "done"} {
		if got := res.Nodes[id].Status; got != StatusOmitted {
			t.Fatalf("%s = %s, want omitted", id, got)
		}
	}
}

func TestRunFailureSkipsDownstreamUnlessTolerated(t *testing.T) {
	w := mustParse(t, chain)
	res := Run(context.Background(), w, &stubRunner{fails: map[string]bool{"a": true}}, Options{})
	if res.Nodes["a"].Status != StatusFailed || res.Nodes["b"].Status != StatusSkipped || res.Nodes["c"].Status != StatusSkipped {
		t.Fatalf("statuses: a=%s b=%s c=%s", res.Nodes["a"].Status, res.Nodes["b"].Status, res.Nodes["c"].Status)
	}
	if !res.Failed(w) {
		t.Fatal("workflow should fail")
	}

	tolerant := strings.Replace(chain, "## a\n", "## a\ncontinue_on_error: true\n\n", 1)
	w = mustParse(t, tolerant)
	r := &stubRunner{fails: map[string]bool{"a": true}, outputs: map[string]string{"b": "B"}}
	res = Run(context.Background(), w, r, Options{})
	if res.Nodes["b"].Status != StatusSucceeded || r.prompts["b"] != "got " {
		t.Fatalf("b = %s prompt %q; want succeeded with empty {{a}}", res.Nodes["b"].Status, r.prompts["b"])
	}
	if res.Failed(w) {
		t.Fatal("a tolerated failure should not fail the workflow")
	}
}

func TestRunCancelKeepsFinishedWork(t *testing.T) {
	w := mustParse(t, chain)
	ctx, cancel := context.WithCancel(context.Background())
	hold := make(chan struct{})
	r := &stubRunner{outputs: map[string]string{"a": "A"}, block: map[string]chan struct{}{"b": hold}}
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	res := Run(ctx, w, r, Options{})

	if res.Nodes["a"].Status != StatusSucceeded || res.Nodes["a"].Output != "A" {
		t.Fatalf("a = %+v, want its finished output kept", res.Nodes["a"])
	}
	if !errors.Is(res.Nodes["b"].Err, context.Canceled) {
		t.Fatalf("b err = %v, want canceled", res.Nodes["b"].Err)
	}
	if !res.Failed(w) {
		t.Fatal("a cancelled run is a failed run")
	}
}

func TestSummaryMarksEachNodesOutcome(t *testing.T) {
	// The summary is what the launching conversation reads back, so its
	// shape is owned here rather than by any end-to-end suite.
	src := "```mermaid\nflowchart LR\n  a --> b --> c\n  a -->|X| d\n```\n\n## a\na\n\n## b\nb\n\n## c\nc\n\n## d\nd\n"
	w := mustParse(t, src)
	res := Run(context.Background(), w, &stubRunner{outputs: map[string]string{"a": "A"}, fails: map[string]bool{"b": true}}, Options{})

	got := res.Summary(w)
	for _, want := range []string{"✓ a", "✗ b", "b exploded", "↷ c", "⊘ d"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary lacks %q:\n%s", want, got)
		}
	}
}
