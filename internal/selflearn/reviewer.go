// Package selflearn implements the self-learning loop. Layer 1 (Reviewer
// in this file) is a per-turn background reviewer that, on cadence, forks
// a restricted agent to capture durable memory and skill updates.
// See notes/active/l1-background-review.md.
//
// The trigger core (cadence, StopEndTurn gate, ≤1-in-flight cap) lives
// here; the fork+review runs through an injected ReviewFunc so the
// trigger stays unit-testable without an LLM.
package selflearn

import (
	"strings"
	"sync"

	"go.uber.org/zap"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/log"
)

// Arm configures one review arm. ResolveSettings applies the cadence
// default, so Interval is positive on a Config built the normal way.
type Arm struct {
	Enabled  bool
	Interval int
}

// ReviewKind is a bitmask of the arms that fired on a given turn.
type ReviewKind uint8

const (
	KindMemory ReviewKind = 1 << iota
	KindSkills
)

// Has reports whether k includes x.
func (k ReviewKind) Has(x ReviewKind) bool { return k&x != 0 }

// String renders the active arms as a stable, log-friendly label. Used by the
// wire-up's review-summary log line.
func (k ReviewKind) String() string {
	var parts []string
	if k.Has(KindMemory) {
		parts = append(parts, "memory")
	}
	if k.Has(KindSkills) {
		parts = append(parts, "skill")
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, "+")
}

// ReviewFunc performs the actual fork+review for the fired arms, given the
// snapshot of the just-completed turn's conversation. It runs on a background
// goroutine and must be best-effort (never panic out / never block the user).
// Injected so trigger logic is unit-testable without an LLM.
type ReviewFunc func(kinds ReviewKind, snapshot []core.Message)

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

// New builds a Reviewer from cfg's arm config. review is invoked (on its
// own goroutine) when an arm's threshold is reached. Disabled arms never fire.
func New(cfg Config, review ReviewFunc) *Reviewer {
	return &Reviewer{
		memEnabled:   cfg.Memory.Enabled,
		skillEnabled: cfg.Skills.Enabled,
		memEvery:     cfg.Memory.Interval,
		skillEvery:   cfg.Skills.Interval,
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
// cancelled / interrupted / max-steps turns are skipped (never review work the
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
		// Cap at 2× threshold so a long-running review can't queue up 50
		// refires from the tool calls that stacked during it.
		if cap := 2 * r.skillEvery; r.itersSinceSkill > cap {
			r.itersSinceSkill = cap
		}
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
		log.Logger().Warn("selflearn: skipping review, a prior review is still running")
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

	// Defensive copy: result.Messages aliases the main agent's live slice,
	// which the main loop may mutate (append, compact-truncate) concurrently
	// with the fork's read. Elements are immutable, so a header copy suffices.
	snapshot := make([]core.Message, len(result.Messages))
	copy(snapshot, result.Messages)

	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Logger().Warn("selflearn: review panicked (recovered)",
					zap.String("kinds", kinds.String()),
					zap.Any("panic", rec),
					zap.Stack("stack"),
				)
			}
			r.mu.Lock()
			r.inFlight = false
			r.mu.Unlock()
		}()
		if r.review != nil {
			r.review(kinds, snapshot)
		}
	}()
}
