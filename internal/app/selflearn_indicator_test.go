package app

import (
	"strings"
	"testing"
	"time"
)

// TestSelfLearnIndicatorPhaseTransitions covers the four-phase state machine
// (idle → reviewing → done/failed → idle) and the per-phase render output.
func TestSelfLearnIndicatorPhaseTransitions(t *testing.T) {
	s := NewSelfLearnIndicator()

	// Idle baseline.
	if snap := s.Snapshot(); snap.Phase != selflearnIdle || snap.Render() != "" {
		t.Fatalf("fresh state should be idle/empty: %+v %q", snap, snap.Render())
	}

	// BeginReview → reviewing, target empty so just spinner.
	s.BeginReview()
	snap := s.Snapshot()
	if snap.Phase != selflearnReviewing {
		t.Fatalf("phase: got %v, want reviewing", snap.Phase)
	}
	// Reviewing render is just the spinner frame ("|", "/", "-", "\\").
	if got := snap.Render(); len(got) != 1 || strings.ContainsAny(got, "evolvedfailedchanges") {
		t.Fatalf("reviewing render without target: %q", got)
	}

	// Complete → done with LLM summary; the closing line lands verbatim.
	s.RecordAction(ReviewAction{Verb: "updated", Kind: "skill", Target: "go-testing"})
	s.RecordAction(ReviewAction{Verb: "saved", Kind: "memory", Target: "debugging"})
	s.Complete("trimmed go-testing by 1.8KB")
	snap = s.Snapshot()
	if snap.Phase != selflearnDone || snap.Changes != 2 {
		t.Fatalf("done snap: %+v", snap)
	}
	if got := snap.Render(); got != "✓ trimmed go-testing by 1.8KB" {
		t.Fatalf("done render: %q", got)
	}

	// Tick before hold expires → stays done.
	if _, active := s.Tick(time.Now()); !active {
		t.Fatal("done state should not decay before hold")
	}
	// Tick after hold expires → idle.
	if _, active := s.Tick(time.Now().Add(selflearnDoneHoldDuration + time.Millisecond)); active {
		t.Fatal("done state should decay after hold")
	}
	if snap := s.Snapshot(); snap.Phase != selflearnIdle {
		t.Fatalf("post-decay phase: %v", snap.Phase)
	}
}

// TestSelfLearnIndicatorFailDecay checks the failed-phase render label and the
// longer hold window before fading back to idle.
func TestSelfLearnIndicatorFailDecay(t *testing.T) {
	s := NewSelfLearnIndicator()
	s.BeginReview()
	s.Fail()
	if got := s.Snapshot().Render(); got != "× review failed" {
		t.Fatalf("fail render: %q", got)
	}
	// Done hold (2 s) would clear by now; failed (3 s) must still be active.
	if _, active := s.Tick(time.Now().Add(selflearnDoneHoldDuration + time.Millisecond)); !active {
		t.Fatal("failed state must outlast the done-hold window")
	}
	// Past the failed hold, decays to idle.
	if _, active := s.Tick(time.Now().Add(selflearnFailedHoldDuration + time.Millisecond)); active {
		t.Fatal("failed state should decay after failed-hold")
	}
}

// TestSelfLearnIndicatorStepDebouncesTarget ensures rapid-fire Step calls within
// the debounce window keep the previously-displayed target, while the next
// Step beyond the window swaps it. The change counter is unaffected — it
// counts every successful write regardless of swap.
func TestSelfLearnIndicatorStepDebouncesTarget(t *testing.T) {
	s := NewSelfLearnIndicator()
	s.BeginReview()
	s.RecordAction(ReviewAction{Verb: "updated", Kind: "skill", Target: "first"})
	if got := s.Snapshot().Target; got != "skill · first" {
		t.Fatalf("initial target: %q", got)
	}
	// Immediate second RecordAction is inside the debounce window.
	s.RecordAction(ReviewAction{Verb: "updated", Kind: "skill", Target: "second"})
	if got := s.Snapshot().Target; got != "skill · first" {
		t.Fatalf("debounced target should stay %q, got %q", "skill · first", got)
	}
	// But the change counter still moved.
	if got := s.Snapshot().Changes; got != 2 {
		t.Fatalf("changes after 2 steps: %d", got)
	}
}

// TestSelfLearnIndicatorTickFrameAdvances checks the braille spinner cycles forward
// on every tick during the reviewing phase.
func TestSelfLearnIndicatorTickFrameAdvances(t *testing.T) {
	s := NewSelfLearnIndicator()
	s.BeginReview()
	frame0 := s.Snapshot().Frame
	_, _ = s.Tick(time.Now())
	frame1 := s.Snapshot().Frame
	if frame1 == frame0 {
		t.Fatalf("Tick should advance the spinner frame: %d → %d", frame0, frame1)
	}
}

// TestFormatRecapBlock locks the post-review recap shape: per-kind
// sub-headers ("memory", "skill") with their action rows indented
// under them, no top "Self-improvement" header, no card frame. Memory
// index writes render as "index" so the column stays aligned;
// created actions get a "new ·" inline marker. Empty input ⇒ "" so
// the wire-up's "skip publish on no writes" branch keeps working.
func TestFormatRecapBlock(t *testing.T) {
	if got := formatRecapBlock(nil); got != "" {
		t.Fatalf("empty input should yield empty string; got %q", got)
	}
	got := formatRecapBlock([]ReviewAction{
		{Verb: "saved", Kind: "memory", Target: "", Note: "noted make ci"},
		{Verb: "saved", Kind: "memory", Target: "debugging", Note: "added 3 entries"},
		{Verb: "updated", Kind: "skill", Target: "go-testing", Note: "trimmed examples"},
		{Verb: "created", Kind: "skill", Target: "python-typing", Note: "typing-hints"},
		{Verb: "retired", Kind: "skill", Target: "outdated-thing"}, // no note
	})
	for _, want := range []string{
		"│",                       // left rail quotes every line
		"memory",                  // kind sub-header
		"· index — noted make ci", // memory index → "index"
		"· debugging — added 3 entries",
		"skill",
		"· go-testing — trimmed examples",
		"· python-typing — typing-hints", // no separate "new ·" marker
		"· outdated-thing",               // note absent → no trailing " — "
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("recap missing %q; got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "new ·") {
		t.Fatalf("recap should no longer carry the redundant \"new ·\" marker; got:\n%s", got)
	}
	if strings.Contains(got, "Self-improvement") {
		t.Fatalf("recap should no longer carry the top header; got:\n%s", got)
	}
}

// TestVerbMapping covers the memory_write / skill_manage → recap verb maps
// so a future action surface stays consistent with the §"User-visible
// surface" example.
func TestVerbMapping(t *testing.T) {
	memCases := map[string]string{"add": "saved", "replace": "replaced", "remove": "removed", "unknown": "unknown"}
	for in, want := range memCases {
		if got := memoryVerb(in); got != want {
			t.Fatalf("memoryVerb(%q): got %q, want %q", in, got, want)
		}
	}
	skillCases := map[string]string{
		"create": "created", "patch": "updated", "edit": "updated",
		"write_file": "extended", "remove_file": "trimmed",
		"delete": "retired", "unknown": "unknown",
	}
	for in, want := range skillCases {
		if got := skillVerb(in); got != want {
			t.Fatalf("skillVerb(%q): got %q, want %q", in, got, want)
		}
	}
}

// TestCompleteSilentOnEmptyActions guards the §6 invariant #7 surface
// promise: a successful review with zero writes must NOT linger in the
// done-hold (no visible "evolved"); the state goes straight to idle so
// the status bar is pixel-identical to a no-review session.
func TestCompleteSilentOnEmptyActions(t *testing.T) {
	s := NewSelfLearnIndicator()
	s.BeginReview()
	// No RecordAction calls — pass produced no writes.
	s.Complete("")
	if snap := s.Snapshot(); snap.Phase != selflearnIdle {
		t.Fatalf("zero-write Complete should go straight to idle; got phase %v", snap.Phase)
	}
}

// TestCompleteCapturesCountBeforeDrain guards the wire-up ordering:
// Complete must capture doneCount = len(s.actions) BEFORE DrainActions
// clears the slice. Otherwise the done-hold renders "evolved" without
// the count.
func TestCompleteCapturesCountBeforeDrain(t *testing.T) {
	s := NewSelfLearnIndicator()
	s.BeginReview()
	s.RecordAction(ReviewAction{Verb: "saved", Kind: "memory", Target: "memory"})
	s.RecordAction(ReviewAction{Verb: "updated", Kind: "skill", Target: "go-testing"})
	s.Complete("")
	if got := s.Snapshot().Changes; got != 2 {
		t.Fatalf("post-Complete Changes: got %d, want 2", got)
	}
	_ = s.DrainActions() // wire-up runs Drain after Complete
	if got := s.Snapshot().Changes; got != 2 {
		t.Fatalf("post-Drain Changes during done-hold: got %d, want 2", got)
	}
}

// TestMemoryTopicName checks the helper that strips the .md suffix and
// returns the bare topic name (or "" for the index file). The indicator
// renderer adds the "memory" / "memory · " prefix at display time.
func TestMemoryTopicName(t *testing.T) {
	cases := map[string]string{
		"":             "",
		"MEMORY.md":    "",
		"memory.md":    "",
		"debugging.md": "debugging",
		"perf.md":      "perf",
	}
	for in, want := range cases {
		if got := memoryTopicName(in); got != want {
			t.Fatalf("memoryTopicName(%q): got %q, want %q", in, got, want)
		}
	}
}
