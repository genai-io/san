// Package selflearn implements the self-learning loop. Layer 1 (this file's
// Reviewer) is a per-turn background reviewer that, on cadence, forks a
// restricted agent to capture durable memory and skill updates; Layer 2 (a
// later Curator) will maintain the collection. See
// notes/active/l1-background-review.md.
//
// This file holds the L1 trigger core — the two-signal cadence, StopEndTurn
// gating, counter reset, and the ≤1-in-flight concurrency cap. The actual
// fork+review is injected as a ReviewFunc so the trigger is testable without an
// LLM; the fork lives alongside.
package selflearn

import (
	"sync"

	"github.com/genai-io/gen-code/internal/core"
	"github.com/genai-io/gen-code/internal/log"
)

// Default cadences (overridable via config). Memory counts user turns; skills
// count tool iterations within a turn.
const (
	DefaultMemoryEveryTurns    = 10
	DefaultSkillEveryToolIters = 10
)

// Arm configures one review arm. Interval <= 0 falls back to the default.
type Arm struct {
	Enabled  bool
	Interval int
}

// Config controls the two independently-toggled review arms.
type Config struct {
	Memory Arm
	Skills Arm
}

// Enabled reports whether any arm is on. When false the caller should not even
// construct a Reviewer (zero overhead).
func (c Config) Enabled() bool { return c.Memory.Enabled || c.Skills.Enabled }

// ReviewKind is a bitmask of the arms that fired on a given turn.
type ReviewKind uint8

const (
	KindMemory ReviewKind = 1 << iota
	KindSkills
)

// Has reports whether k includes x.
func (k ReviewKind) Has(x ReviewKind) bool { return k&x != 0 }

// ReviewFunc performs the actual fork+review for the fired arms. It runs on a
// background goroutine and must be best-effort (never panic out / never block
// the user). Injected so trigger logic is unit-testable without an LLM.
type ReviewFunc func(kinds ReviewKind)

// Reviewer owns the per-session counters and fires reviews on cadence. Safe for
// concurrent use; Observe is the only entry point.
type Reviewer struct {
	memEnabled   bool
	skillEnabled bool
	memEvery     int
	skillEvery   int
	review       ReviewFunc

	mu               sync.Mutex
	turnsSinceMemory int
	itersSinceSkill  int
	inFlight         bool
}

// New builds a Reviewer from cfg. review is invoked (on its own goroutine) when
// an arm's threshold is reached. Disabled arms never fire.
func New(cfg Config, review ReviewFunc) *Reviewer {
	return &Reviewer{
		memEnabled:   cfg.Memory.Enabled,
		skillEnabled: cfg.Skills.Enabled,
		memEvery:     positiveOr(cfg.Memory.Interval, DefaultMemoryEveryTurns),
		skillEvery:   positiveOr(cfg.Skills.Interval, DefaultSkillEveryToolIters),
		review:       review,
	}
}

// SeedTurns hydrates the memory counter on session resume so cadence survives a
// process restart (invariant #8). priorUserTurns is the count of user turns
// already in the resumed history.
func (r *Reviewer) SeedTurns(priorUserTurns int) {
	if !r.memEnabled || r.memEvery <= 0 {
		return
	}
	r.mu.Lock()
	r.turnsSinceMemory = priorUserTurns % r.memEvery
	r.mu.Unlock()
}

// Observe processes one completed turn. Only cleanly-ended turns count;
// cancelled / interrupted / max-turns turns are skipped (never review work the
// user abandoned). When an arm reaches its threshold it fires a review on a
// background goroutine, at most one in flight per Reviewer — a trigger that
// arrives while a prior review runs is dropped (and the counter NOT reset, so
// it fires again next turn) rather than queued.
func (r *Reviewer) Observe(result core.Result) {
	if result.StopReason != core.StopEndTurn {
		return
	}

	r.mu.Lock()
	if r.memEnabled {
		r.turnsSinceMemory++
	}
	if r.skillEnabled {
		r.itersSinceSkill += result.ToolUses
	}

	var kinds ReviewKind
	if r.memEnabled && r.turnsSinceMemory >= r.memEvery {
		kinds |= KindMemory
	}
	if r.skillEnabled && r.itersSinceSkill >= r.skillEvery {
		kinds |= KindSkills
	}
	if kinds == 0 {
		r.mu.Unlock()
		return
	}
	if r.inFlight {
		// Drop, don't reset: the threshold stays tripped and fires again on
		// the next clean turn once the prior review finishes.
		r.mu.Unlock()
		log.Logger().Warn("l1: skipping review, a prior review is still running")
		return
	}
	r.inFlight = true
	if kinds.Has(KindMemory) {
		r.turnsSinceMemory = 0
	}
	if kinds.Has(KindSkills) {
		r.itersSinceSkill = 0
	}
	r.mu.Unlock()

	go r.run(kinds)
}

func (r *Reviewer) run(kinds ReviewKind) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Logger().Warn("l1: review panicked (recovered)")
		}
		r.mu.Lock()
		r.inFlight = false
		r.mu.Unlock()
	}()
	if r.review != nil {
		r.review(kinds)
	}
}

func positiveOr(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}
