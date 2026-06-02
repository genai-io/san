// SelfLearnIndicator drives the four-phase status-bar surface (idle /
// reviewing / done / failed). Mutated from the reviewer goroutine
// (BeginReview / RecordAction / Complete / Fail) and the tea Update
// goroutine (Tick); a single mutex serialises both, with an atomic
// phase mirror for the lock-free idle-render fast path.
// See notes/active/l1-background-review.md §"User-visible surface".
package app

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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
// tool calls (not model narration). Target is the bare identifier
// (memory topic name, skill name) — the renderer adds the "memory · "
// / "skill · " prefix at display time, so the kind+target are
// formatted consistently across in-progress and done-summary lines.
// Note is the LLM-supplied short description of what THIS specific
// write changed (e.g. "removed vague tooling guidance"). It surfaces
// in the per-action recap row so the user sees what was done.
type ReviewAction struct {
	Verb   string // "saved" | "replaced" | "removed" | "updated" | "extended" | "retired" | "created" | "trimmed"
	Kind   string // "memory" or "skill"
	Target string // skill name, memory topic, or "" for the memory index file
	Note   string // LLM-supplied "what changed" clause
}

// actionLabel formats a single action's "kind · target" for the
// spinner-tail display and the one-line done summary. The "memory"
// kind elides the separator when the target is the index file.
func actionLabel(a ReviewAction) string {
	switch a.Kind {
	case "skill":
		if a.Target == "" {
			return "skill"
		}
		return "skill · " + a.Target
	case "memory":
		if a.Target == "" {
			return "memory"
		}
		return "memory · " + a.Target
	default:
		if a.Target == "" {
			return a.Kind
		}
		return a.Kind + " · " + a.Target
	}
}

// buildDoneSummary turns the per-pass action log into a one-line phrase
// for the done-phase status bar. Rules:
//   - 1 action: full "<verb> <kind · target>", e.g. "saved memory · debugging"
//   - 2-3 distinct actions, fits ~60 chars: list them, comma-separated
//   - otherwise: per-kind grouped counts, e.g. "saved 2 memory entries,
//     updated 1 skill, created 1 skill"
//
// Falls back to "" when actions is empty; the caller's render path uses
// the "N changes" plural form as a final backstop.
func buildDoneSummary(actions []ReviewAction) string {
	if len(actions) == 0 {
		return ""
	}
	if len(actions) == 1 {
		return actions[0].Verb + " " + actionLabel(actions[0])
	}
	if len(actions) <= 3 {
		parts := make([]string, len(actions))
		for i, a := range actions {
			parts[i] = a.Verb + " " + actionLabel(a)
		}
		joined := strings.Join(parts, ", ")
		if len([]rune(joined)) <= 60 {
			return joined
		}
	}
	// 4+ actions or too long for one line — group by (verb, kind) and
	// emit "<verb> N <kind>(s)" phrases.
	type key struct{ verb, kind string }
	type group struct {
		k     key
		count int
	}
	var groups []group
	idx := map[key]int{}
	for _, a := range actions {
		k := key{a.Verb, a.Kind}
		if i, ok := idx[k]; ok {
			groups[i].count++
		} else {
			idx[k] = len(groups)
			groups = append(groups, group{k: k, count: 1})
		}
	}
	parts := make([]string, len(groups))
	for i, g := range groups {
		parts[i] = fmt.Sprintf("%s %d %s", g.k.verb, g.count, pluralKind(g.k.kind, g.count))
	}
	return strings.Join(parts, ", ")
}

// pluralKind returns the noun for a count of N items of the given kind.
// memory → "memory entry"/"memory entries"; skill → "skill"/"skills".
func pluralKind(kind string, n int) string {
	switch kind {
	case "memory":
		if n == 1 {
			return "memory entry"
		}
		return "memory entries"
	case "skill":
		if n == 1 {
			return "skill"
		}
		return "skills"
	default:
		return kind
	}
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
	frame         int            // spinner frame index
	actions       []ReviewAction // recap action log for the current pass
	doneCount     int            // captures len(actions) at Complete so the done-hold render survives DrainActions
	doneSummary   string         // one-line recap captured at Complete so the done phase reads "✓ updated go-testing" rather than "✓ 4 changes"
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
	s.doneSummary = ""
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
		s.target = actionLabel(act)
		s.lastSwap = now
	}
}

// Complete is the success transition. summary is the closing line the
// reviewer LLM produced ("trimmed go-testing SKILL.md by 1.8KB"); empty
// or too long → fall back to a templated phrase built from the action
// log. Must run BEFORE DrainActions so doneCount captures len(actions);
// a zero-write pass goes straight to idle (§6 invariant #7).
func (s *SelfLearnIndicator) Complete(summary string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.target = ""
	s.doneCount = len(s.actions)
	if s.doneCount == 0 {
		s.phase = selflearnIdle
		s.phaseAtomic.Store(int32(selflearnIdle))
		return
	}
	s.doneSummary = pickSummary(summary, s.actions)
	s.phase = selflearnDone
	s.phaseAtomic.Store(int32(selflearnDone))
	s.enteredAt = time.Now()
}

// pickSummary returns the LLM-supplied line when it's present and short
// enough for a status bar; otherwise falls back to the templated
// action-log summary.
func pickSummary(llmText string, actions []ReviewAction) string {
	if s := firstLine(strings.TrimSpace(llmText)); s != "" && len([]rune(s)) <= 80 {
		return s
	}
	return buildDoneSummary(actions)
}

// firstLine returns s up to the first newline, with trailing
// whitespace and a trailing period trimmed (the prompt asks for "no
// period" but we sanitize anyway).
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimRight(strings.TrimSpace(s), ".")
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
		s.frame = (s.frame + 1) % len(asciiSpinnerFrames)
		return selflearnTickInterval, true
	case selflearnDone:
		return s.decayPhase(now, selflearnDoneHoldDuration)
	case selflearnFailed:
		return s.decayPhase(now, selflearnFailedHoldDuration)
	default:
		s.phaseAtomic.Store(int32(selflearnIdle))
		s.tickerRunning = false
		return 0, false
	}
}

// decayPhase returns the remaining hold time, or transitions to idle and
// returns (0, false) once the hold elapses. Callers hold s.mu.
func (s *SelfLearnIndicator) decayPhase(now time.Time, hold time.Duration) (time.Duration, bool) {
	remaining := hold - now.Sub(s.enteredAt)
	if remaining > 0 {
		return remaining, true
	}
	s.phase = selflearnIdle
	s.phaseAtomic.Store(int32(selflearnIdle))
	s.tickerRunning = false
	return 0, false
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
		Summary: s.doneSummary,
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
	Summary string // one-line recap shown during the done phase
}

// asciiSpinnerFrames is the classic four-frame ASCII spinner. We pick
// this over the braille set so the status line stays terminal-portable
// (some PTYs render braille glyphs as wide cells, which jitters the
// width of the surrounding label).
var asciiSpinnerFrames = []string{"|", "/", "-", `\`}

// Render returns the status-bar label; "" for idle.
func (s SelfLearnIndicatorSnapshot) Render() string {
	switch s.Phase {
	case selflearnReviewing:
		spinner := asciiSpinnerFrames[s.Frame%len(asciiSpinnerFrames)]
		if s.Target == "" {
			return spinner
		}
		return spinner + " " + s.Target
	case selflearnDone:
		if s.Changes == 0 {
			return ""
		}
		if s.Summary != "" {
			return "✓ " + s.Summary
		}
		return fmt.Sprintf("✓ %d changes", s.Changes)
	case selflearnFailed:
		return "× review failed"
	default:
		return ""
	}
}
