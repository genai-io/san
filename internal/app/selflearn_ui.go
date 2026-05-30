// L1 self-learning runtime UI state.
//
// SelfLearnUIState drives the four-phase status-bar surface described in
// notes/active/l1-background-review.md §"User-visible surface":
//
//   - idle        (hidden)
//   - reviewing   ("evolving <braille> [<target>]") while a fork is in flight
//   - done        ("evolved · N changes") for 2 s after a successful pass
//   - failed      ("evolving failed") for 3 s after a failed pass
//
// The state is updated from two goroutines:
//
//   - The reviewer fork's goroutine, via BeginReview / Step / Complete / Fail.
//   - The Bubble Tea Update goroutine, via Tick (which advances the spinner
//     frame and decays done/failed back to idle once their hold expires).
//
// All transitions go through a single sync.Mutex so the Snapshot the render
// path reads is always a consistent triple of (phase, target, changes).
package app

import (
	"fmt"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// selflearnTickMsg is the periodic tea.Msg that advances the spinner frame
// and decays done/failed phases back to idle. Scheduled the first time
// when the wire-up publishes a "selflearn.review.started" hub event; the
// dispatcher re-arms it for as long as the UI state reports active.
type selflearnTickMsg struct{}

// scheduleSelflearnTick returns the Cmd that fires the next tick after
// selflearnTickInterval.
func scheduleSelflearnTick() tea.Cmd {
	return tea.Tick(selflearnTickInterval, func(time.Time) tea.Msg {
		return selflearnTickMsg{}
	})
}

// brailleSpinnerFrames is the ten-frame braille spinner used while a review
// fork is in flight. Standard terminal-spinner glyphs (not emoji), chosen
// because they render in every monospace font and animate smoothly.
var brailleSpinnerFrames = []string{
	"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏",
}

// selflearnTickInterval is the spinner cadence. ~100 ms reads as a smooth
// rotation without burning CPU; matches the provider-connect spinner.
const selflearnTickInterval = 100 * time.Millisecond

const (
	// selflearnDoneHoldDuration is how long "evolved · N changes" stays
	// visible after a successful pass before fading back to idle (§UI table).
	selflearnDoneHoldDuration = 2 * time.Second
	// selflearnFailedHoldDuration is the longer hold for failure states so
	// the user has time to see the failure marker.
	selflearnFailedHoldDuration = 3 * time.Second
	// selflearnTargetDebounce is the minimum time the status bar holds the
	// current target before swapping. Prevents flicker when the fork makes
	// rapid tool calls (§"Runtime — status bar" debounce ≥ 400 ms).
	selflearnTargetDebounce = 400 * time.Millisecond
)

type selflearnPhase int

const (
	selflearnIdle selflearnPhase = iota
	selflearnReviewing
	selflearnDone
	selflearnFailed
)

// SelfLearnUIState is the live UI-side state for the L1 indicator. Held by
// pointer on services so all goroutines mutate the same instance.
type SelfLearnUIState struct {
	mu sync.Mutex

	phase     selflearnPhase
	target    string    // current target shown next to the spinner
	frame     int       // braille frame index
	changes   int       // count of writes the current pass has made
	enteredAt time.Time // for done/failed auto-decay
	lastSwap  time.Time // for target-swap debounce
}

// NewSelfLearnUIState returns a fresh idle state.
func NewSelfLearnUIState() *SelfLearnUIState { return &SelfLearnUIState{} }

// BeginReview transitions to the reviewing phase and resets per-pass
// counters. Called from the ReviewFunc the moment the fork is about to run.
func (s *SelfLearnUIState) BeginReview() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phase = selflearnReviewing
	s.target = ""
	s.frame = 0
	s.changes = 0
	s.enteredAt = time.Now()
	s.lastSwap = time.Time{}
}

// Step records a successful write of `target` (a skill name or
// "memory · <topic>"). Increments the change counter; updates the displayed
// target unless the previous swap is still inside the debounce window — in
// which case the old target stays put to avoid flicker.
func (s *SelfLearnUIState) Step(target string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.changes++
	now := time.Now()
	if s.lastSwap.IsZero() || now.Sub(s.lastSwap) >= selflearnTargetDebounce {
		s.target = target
		s.lastSwap = now
	}
}

// Complete transitions to the done phase. The current change count is
// preserved so the "evolved · N changes" tail can show it.
func (s *SelfLearnUIState) Complete() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phase = selflearnDone
	s.target = ""
	s.enteredAt = time.Now()
}

// Fail transitions to the failed phase, clearing the target so the
// "evolving failed" label reads cleanly.
func (s *SelfLearnUIState) Fail() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phase = selflearnFailed
	s.target = ""
	s.enteredAt = time.Now()
}

// Tick advances the spinner frame and decays done/failed phases that have
// held longer than their hold duration. Returns true if the state is still
// non-idle (so the caller can re-arm the tick) and false if it just
// transitioned to idle (caller stops re-arming).
func (s *SelfLearnUIState) Tick(now time.Time) (stillActive bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.phase {
	case selflearnReviewing:
		s.frame = (s.frame + 1) % len(brailleSpinnerFrames)
		return true
	case selflearnDone:
		if now.Sub(s.enteredAt) >= selflearnDoneHoldDuration {
			s.phase = selflearnIdle
			s.changes = 0
			return false
		}
		return true
	case selflearnFailed:
		if now.Sub(s.enteredAt) >= selflearnFailedHoldDuration {
			s.phase = selflearnIdle
			return false
		}
		return true
	default:
		return false
	}
}

// Snapshot returns a consistent read of the state for the render path. The
// returned value is copy-by-value so the render goroutine can use it
// without locking.
func (s *SelfLearnUIState) Snapshot() SelfLearnUISnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return SelfLearnUISnapshot{
		Phase:   s.phase,
		Target:  s.target,
		Frame:   s.frame,
		Changes: s.changes,
	}
}

// SelfLearnUISnapshot is the read-only view of the state taken at render
// time. Render() turns it into the actual status-bar segment, or "" when
// the phase is idle.
type SelfLearnUISnapshot struct {
	Phase   selflearnPhase
	Target  string
	Frame   int
	Changes int
}

// IsActive reports whether the snapshot represents a non-idle phase. Used
// by the wire-up to decide whether to re-arm the tick.
func (s SelfLearnUISnapshot) IsActive() bool { return s.Phase != selflearnIdle }

// Render returns the status-bar segment for the snapshot. Empty for idle so
// renderModeStatus can simply concatenate without a branch.
func (s SelfLearnUISnapshot) Render() string {
	switch s.Phase {
	case selflearnReviewing:
		spinner := brailleSpinnerFrames[s.Frame%len(brailleSpinnerFrames)]
		if s.Target == "" {
			return "evolving " + spinner
		}
		return "evolving " + spinner + " " + s.Target
	case selflearnDone:
		if s.Changes == 0 {
			return "evolved"
		}
		return fmt.Sprintf("evolved · %d changes", s.Changes)
	case selflearnFailed:
		return "evolving failed"
	default:
		return ""
	}
}
