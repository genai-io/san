package app

import (
	"fmt"
	"testing"
	"time"
)

// TestSelfLearnIndicatorPreview prints every phase of the indicator so
// the status-line look is inspectable via
//
//	go test -v -run TestSelfLearnIndicatorPreview ./internal/app/
//
// It exercises the visible surface (spinner frames, target tail,
// LLM-supplied summary, action-log fallback, failed render).
func TestSelfLearnIndicatorPreview(t *testing.T) {
	if !testing.Verbose() {
		t.Skip("preview only printed in verbose mode")
	}

	row := func(label, body string) {
		fmt.Printf("  %-32s → %q\n", label, body)
	}

	// ── Reviewing: spinner alone, then with formatted target ────────────
	fmt.Println("\nReviewing phase (status bar tail):")
	s := NewSelfLearnIndicator()
	s.BeginReview()
	row("just spun up", s.Snapshot().Render())

	for range 3 {
		_, _ = s.Tick(time.Now())
	}
	row("after 3 ticks", s.Snapshot().Render())

	s.RecordAction(ReviewAction{Verb: "saved", Kind: "memory", Target: ""})
	row("memory index write", s.Snapshot().Render())

	// Past debounce, swap to a topic file.
	time.Sleep(selflearnTargetDebounce + 10*time.Millisecond)
	s.RecordAction(ReviewAction{Verb: "saved", Kind: "memory", Target: "debugging"})
	row("memory · debugging", s.Snapshot().Render())

	time.Sleep(selflearnTargetDebounce + 10*time.Millisecond)
	s.RecordAction(ReviewAction{Verb: "updated", Kind: "skill", Target: "go-testing"})
	row("skill · go-testing", s.Snapshot().Render())

	// ── Done phase: LLM-supplied summary ────────────────────────────────
	fmt.Println("\nDone phase — LLM-supplied summary:")
	{
		s := NewSelfLearnIndicator()
		s.BeginReview()
		s.RecordAction(ReviewAction{Verb: "updated", Kind: "skill", Target: "go-testing"})
		s.Complete("trimmed go-testing SKILL.md by 1.8KB")
		row("single skill edit", s.Snapshot().Render())
	}
	{
		s := NewSelfLearnIndicator()
		s.BeginReview()
		s.RecordAction(ReviewAction{Verb: "saved", Kind: "memory", Target: "debugging"})
		s.RecordAction(ReviewAction{Verb: "saved", Kind: "memory", Target: "perf"})
		s.RecordAction(ReviewAction{Verb: "created", Kind: "skill", Target: "python-typing"})
		s.Complete("saved 2 debug notes + new python-typing skill")
		row("multi-action LLM line", s.Snapshot().Render())
	}

	// ── Done phase: empty LLM summary falls back to action-log template ─
	fmt.Println("\nDone phase — fallback when LLM is silent:")
	for _, tc := range []struct {
		name    string
		actions []ReviewAction
	}{
		{"1 action", []ReviewAction{
			{Verb: "updated", Kind: "skill", Target: "go-testing"},
		}},
		{"2 actions", []ReviewAction{
			{Verb: "saved", Kind: "memory", Target: "debugging"},
			{Verb: "updated", Kind: "skill", Target: "go-testing"},
		}},
		{"3 actions", []ReviewAction{
			{Verb: "saved", Kind: "memory", Target: "debugging"},
			{Verb: "updated", Kind: "skill", Target: "go-testing"},
			{Verb: "created", Kind: "skill", Target: "python-typing"},
		}},
		{"4+ actions (grouped)", []ReviewAction{
			{Verb: "saved", Kind: "memory", Target: "debugging"},
			{Verb: "saved", Kind: "memory", Target: "perf"},
			{Verb: "updated", Kind: "skill", Target: "go-testing"},
			{Verb: "created", Kind: "skill", Target: "python-typing"},
		}},
	} {
		s := NewSelfLearnIndicator()
		s.BeginReview()
		for _, a := range tc.actions {
			s.RecordAction(a)
		}
		s.Complete("")
		row(tc.name, s.Snapshot().Render())
	}

	// ── Failed phase ────────────────────────────────────────────────────
	fmt.Println("\nFailed phase:")
	{
		s := NewSelfLearnIndicator()
		s.BeginReview()
		s.Fail()
		row("review failed", s.Snapshot().Render())
	}
	fmt.Println()
}
