// SelfLearnIndicator drives the four-phase status-bar surface (idle /
// reviewing / done / failed). Mutated from the reviewer goroutine
// (BeginReview / RecordAction / Complete / Fail) and the tea Update
// goroutine (Tick); a single mutex serialises both, with an atomic
// phase mirror for the lock-free idle-render fast path.
// See notes/active/l1-background-review.md §"User-visible surface".
package app

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/genai-io/gen-code/internal/app/kit"
)

// selflearnTickMsg advances the spinner frame and decays done/failed back
// to idle. The dispatcher re-arms it while the state is non-idle.
type selflearnTickMsg struct{}

func scheduleSelflearnTick() tea.Cmd {
	return tea.Tick(selflearnTickInterval, func(time.Time) tea.Msg { return selflearnTickMsg{} })
}

const (
	selflearnTickInterval = 100 * time.Millisecond // spinner cadence (matches provider-connect)

	selflearnDoneHoldDuration   = 2 * time.Second // "evolved · N changes" visibility
	selflearnFailedHoldDuration = 3 * time.Second // longer so failures stay readable
	selflearnTargetDebounce     = 400 * time.Millisecond
)

type selflearnPhase int

const (
	selflearnIdle selflearnPhase = iota
	selflearnReviewing
	selflearnDone
	selflearnFailed
)

// ReviewAction is one row of the post-pass recap, built from actual
// tool calls (not model narration).
type ReviewAction struct {
	Verb   string // "saved" | "replaced" | "removed" | "updated" | "extended" | "retired" | "created"
	Kind   string // "memory" or "skill"
	Target string // skill name, "memory", or "memory · <topic>"
}

// SelfLearnIndicator is the live UI-side state for the L1 indicator. Held by
// pointer on services so all goroutines mutate the same instance.
type SelfLearnIndicator struct {
	mu sync.Mutex

	// phaseAtomic mirrors phase as an int32 for lock-free reads on the
	// render hot path: TUI repaint frequency × idle steady state would
	// otherwise hammer the mutex for nothing. Writers (BeginReview,
	// Complete, Fail, Tick decay) update this together with phase under
	// the mutex; Snapshot returns the empty value when this reads idle.
	phaseAtomic atomic.Int32

	phase         selflearnPhase
	target        string         // current target shown next to the spinner
	frame         int            // braille frame index
	actions       []ReviewAction // recap action log for the current pass
	doneCount     int            // captures len(actions) at Complete so the done-hold render survives DrainActions
	enteredAt     time.Time      // for done/failed auto-decay
	lastSwap      time.Time      // for target-swap debounce
	tickerRunning bool           // tick chain is live; prevents back-to-back reviews stacking parallel chains
}

func NewSelfLearnIndicator() *SelfLearnIndicator { return &SelfLearnIndicator{} }

// BeginReview enters the reviewing phase and clears per-pass state. Called
// from the ReviewFunc before any tool call fires.
func (s *SelfLearnIndicator) BeginReview() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phase = selflearnReviewing
	s.phaseAtomic.Store(int32(selflearnReviewing))
	s.target = ""
	s.frame = 0
	s.actions = nil
	s.doneCount = 0
	s.enteredAt = time.Now()
	s.lastSwap = time.Time{}
}

// RecordAction logs one successful tool call. Appends to the recap log and
// swaps the spinner-tail target (subject to debounce so rapid writes don't
// flicker the bar).
func (s *SelfLearnIndicator) RecordAction(act ReviewAction) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.actions = append(s.actions, act)
	now := time.Now()
	if s.lastSwap.IsZero() || now.Sub(s.lastSwap) >= selflearnTargetDebounce {
		s.target = act.Target
		s.lastSwap = now
	}
}

// Complete is the success transition. Must run BEFORE DrainActions so
// doneCount captures len(s.actions); a zero-write pass goes straight to
// idle (§6 invariant #7 — silent when nothing changed).
func (s *SelfLearnIndicator) Complete() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.target = ""
	s.doneCount = len(s.actions)
	if s.doneCount == 0 {
		s.phase = selflearnIdle
		s.phaseAtomic.Store(int32(selflearnIdle))
		return
	}
	s.phase = selflearnDone
	s.phaseAtomic.Store(int32(selflearnDone))
	s.enteredAt = time.Now()
}

// Fail is called when the fork errors or times out.
func (s *SelfLearnIndicator) Fail() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phase = selflearnFailed
	s.phaseAtomic.Store(int32(selflearnFailed))
	s.target = ""
	s.enteredAt = time.Now()
}

// Tick advances the spinner and decays done/failed. Returns the next tick
// delay (spinner cadence while reviewing; REMAINING hold while done/failed
// so the dispatcher schedules one deadline tick instead of polling); 0 +
// false when the state went idle.
func (s *SelfLearnIndicator) Tick(now time.Time) (delay time.Duration, stillActive bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.phase {
	case selflearnReviewing:
		s.frame = (s.frame + 1) % len(kit.BrailleSpinnerFrames)
		return selflearnTickInterval, true
	case selflearnDone:
		remaining := selflearnDoneHoldDuration - now.Sub(s.enteredAt)
		if remaining <= 0 {
			s.phase = selflearnIdle
			s.phaseAtomic.Store(int32(selflearnIdle))
			s.tickerRunning = false
			return 0, false
		}
		return remaining, true
	case selflearnFailed:
		remaining := selflearnFailedHoldDuration - now.Sub(s.enteredAt)
		if remaining <= 0 {
			s.phase = selflearnIdle
			s.phaseAtomic.Store(int32(selflearnIdle))
			s.tickerRunning = false
			return 0, false
		}
		return remaining, true
	default:
		s.phaseAtomic.Store(int32(selflearnIdle))
		s.tickerRunning = false
		return 0, false
	}
}

// Snapshot reads the state for rendering. Idle fast-paths lock-free via
// phaseAtomic (writers update it together with phase).
func (s *SelfLearnIndicator) Snapshot() SelfLearnIndicatorSnapshot {
	if selflearnPhase(s.phaseAtomic.Load()) == selflearnIdle {
		return SelfLearnIndicatorSnapshot{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return SelfLearnIndicatorSnapshot{
		Phase:   s.phase,
		Target:  s.target,
		Frame:   s.frame,
		Changes: s.changesForRender(),
	}
}

// changesForRender returns live len(actions) while reviewing; during done
// it returns doneCount so the hold survives DrainActions.
func (s *SelfLearnIndicator) changesForRender() int {
	if s.phase == selflearnDone {
		return s.doneCount
	}
	return len(s.actions)
}

// TryStartTicker claims the single tick chain. Returns true if the caller
// should schedule a tick; false if a chain is already live (so back-to-back
// reviews don't stack parallel chains and multiply the spinner cadence).
func (s *SelfLearnIndicator) TryStartTicker() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tickerRunning {
		return false
	}
	s.tickerRunning = true
	return true
}

// DrainActions returns and clears the current pass's action log.
func (s *SelfLearnIndicator) DrainActions() []ReviewAction {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.actions
	s.actions = nil
	return out
}

// SelfLearnIndicatorSnapshot is the immutable view used by Render.
type SelfLearnIndicatorSnapshot struct {
	Phase   selflearnPhase
	Target  string
	Frame   int
	Changes int
}

// Render returns the status-bar label; "" for idle.
func (s SelfLearnIndicatorSnapshot) Render() string {
	switch s.Phase {
	case selflearnReviewing:
		spinner := kit.BrailleSpinnerFrames[s.Frame%len(kit.BrailleSpinnerFrames)]
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
