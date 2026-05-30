package app

import (
	"strings"
	"testing"
	"time"
)

// TestSelfLearnUIPhaseTransitions covers the four-phase state machine
// (idle → reviewing → done/failed → idle) and the per-phase render output.
func TestSelfLearnUIPhaseTransitions(t *testing.T) {
	s := NewSelfLearnUIState()

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
	if !strings.HasPrefix(snap.Render(), "evolving ") || strings.Contains(snap.Render(), "  ") {
		t.Fatalf("reviewing render without target: %q", snap.Render())
	}

	// Complete → done with change count.
	s.Step("go-testing")
	s.Step("memory · debugging")
	s.Complete()
	snap = s.Snapshot()
	if snap.Phase != selflearnDone || snap.Changes != 2 {
		t.Fatalf("done snap: %+v", snap)
	}
	if got := snap.Render(); got != "evolved · 2 changes" {
		t.Fatalf("done render: %q", got)
	}

	// Tick before hold expires → stays done.
	if !s.Tick(time.Now()) {
		t.Fatal("done state should not decay before hold")
	}
	// Tick after hold expires → idle.
	if s.Tick(time.Now().Add(selflearnDoneHoldDuration + time.Millisecond)) {
		t.Fatal("done state should decay after hold")
	}
	if snap := s.Snapshot(); snap.Phase != selflearnIdle {
		t.Fatalf("post-decay phase: %v", snap.Phase)
	}
}

// TestSelfLearnUIFailDecay checks the failed-phase render label and the
// longer hold window before fading back to idle.
func TestSelfLearnUIFailDecay(t *testing.T) {
	s := NewSelfLearnUIState()
	s.BeginReview()
	s.Fail()
	if got := s.Snapshot().Render(); got != "evolving failed" {
		t.Fatalf("fail render: %q", got)
	}
	// Done hold (2 s) would clear by now; failed (3 s) must still be active.
	if !s.Tick(time.Now().Add(selflearnDoneHoldDuration + time.Millisecond)) {
		t.Fatal("failed state must outlast the done-hold window")
	}
	// Past the failed hold, decays to idle.
	if s.Tick(time.Now().Add(selflearnFailedHoldDuration + time.Millisecond)) {
		t.Fatal("failed state should decay after failed-hold")
	}
}

// TestSelfLearnUIStepDebouncesTarget ensures rapid-fire Step calls within
// the debounce window keep the previously-displayed target, while the next
// Step beyond the window swaps it. The change counter is unaffected — it
// counts every successful write regardless of swap.
func TestSelfLearnUIStepDebouncesTarget(t *testing.T) {
	s := NewSelfLearnUIState()
	s.BeginReview()
	s.Step("first")
	if got := s.Snapshot().Target; got != "first" {
		t.Fatalf("initial target: %q", got)
	}
	// Immediate second Step is inside the debounce window.
	s.Step("second")
	if got := s.Snapshot().Target; got != "first" {
		t.Fatalf("debounced target should stay %q, got %q", "first", got)
	}
	// But the change counter still moved.
	if got := s.Snapshot().Changes; got != 2 {
		t.Fatalf("changes after 2 steps: %d", got)
	}
}

// TestSelfLearnUITickFrameAdvances checks the braille spinner cycles forward
// on every tick during the reviewing phase.
func TestSelfLearnUITickFrameAdvances(t *testing.T) {
	s := NewSelfLearnUIState()
	s.BeginReview()
	frame0 := s.Snapshot().Frame
	s.Tick(time.Now())
	frame1 := s.Snapshot().Frame
	if frame1 == frame0 {
		t.Fatalf("Tick should advance the spinner frame: %d → %d", frame0, frame1)
	}
}

// TestMemoryTopicSuffixStrips checks the helper that produces the bit shown
// after "memory" in the status line: empty when writing the index, " · X"
// when writing a topic file.
func TestMemoryTopicSuffix(t *testing.T) {
	cases := map[string]string{
		"":             "",
		"MEMORY.md":    "",
		"memory.md":    "",
		"debugging.md": " · debugging",
		"perf.md":      " · perf",
	}
	for in, want := range cases {
		if got := memoryTopicSuffix(in); got != want {
			t.Fatalf("memoryTopicSuffix(%q): got %q, want %q", in, got, want)
		}
	}
}
