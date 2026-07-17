// Package selflearn implements the self-learning loop. Layer 1 (Reviewer
// in this file) is a background reviewer that, when the model requests it, forks
// a restricted agent to capture durable memory and skill updates.
// See notes/active/l1-background-review.md.
//
// The trigger core (model request, StopEndTurn gate, ≤1-in-flight cap) lives
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
// skill actions this pass may take and the snapshot of the just-completed
// turn's conversation. It runs on a background goroutine and must be
// best-effort (never panic out / never block the user). Injected so trigger
// logic is unit-testable without an LLM.
type ReviewFunc func(kinds ReviewKind, skillPerms SkillPermissions, snapshot []core.Message)

// Reviewer fires a self-learning review when the model requests one. Safe for
// concurrent use; Observe is the only entry point.
type Reviewer struct {
	memoryEnabled bool
	skillPerms    SkillPermissions
	review        ReviewFunc

	mu       sync.Mutex
	inFlight bool
}

// New builds a Reviewer from cfg's arm config. review is invoked on its own
// goroutine when the model requests a review (via the Evolve tool). Disabled
// arms never fire.
func New(cfg Config, review ReviewFunc) *Reviewer {
	return &Reviewer{
		memoryEnabled: cfg.MemoryEnabled,
		skillPerms:    cfg.Skills,
		review:        review,
	}
}

// Observe processes one completed turn. Triggering is model-decided: a review
// fires only when the main agent called the Evolve tool this turn
// (evolveRequested). It then covers every enabled arm — memory if enabled, and
// skills scoped by skillUsed (a skill-use turn weighs update/delete of that
// skill, a skill-free turn weighs create), bounded by the permission gates.
//
// Only cleanly-ended turns count; cancelled / interrupted / max-steps turns are
// skipped (never review work the user abandoned). At most one review is in
// flight per Reviewer — a request arriving while a prior review runs is dropped
// rather than queued.
func (r *Reviewer) Observe(result core.Result, skillUsed, evolveRequested bool) {
	if result.StopReason != core.StopEndTurn || !evolveRequested {
		return
	}

	// Scope the skills pass by the objective fact of skill use so the reviewer
	// prompt stays accurate; the reviewer decides the specific action within it.
	var skillPerms SkillPermissions
	if skillUsed {
		skillPerms.AllowUpdate = r.skillPerms.AllowUpdate
		skillPerms.AllowDelete = r.skillPerms.AllowDelete
	} else {
		skillPerms.AllowCreate = r.skillPerms.AllowCreate
	}

	var kinds ReviewKind
	if r.memoryEnabled {
		kinds |= KindMemory
	}
	if skillPerms.Any() {
		kinds |= KindSkills
	}
	if kinds == 0 {
		return
	}

	r.mu.Lock()
	if r.inFlight {
		r.mu.Unlock()
		log.Logger().Warn("selflearn: skipping review, a prior review is still running")
		return
	}
	r.inFlight = true
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
			r.review(kinds, skillPerms, snapshot)
		}
	}()
}
