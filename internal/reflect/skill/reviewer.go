// Package skill implements the Auto Skill trigger and skill persistence.
package skill

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/infra/log"
)

// ReviewFunc runs an Auto Skill review on a background goroutine, scoped to
// the allowed skill actions and the just-completed turn. It is injected so
// trigger logic can be tested without an LLM.
type ReviewFunc func(skillPerms SkillPermissions, snapshot []core.Message)

// Reviewer fires a self-learning review when the model requests one. Safe for
// concurrent use; Observe is the only entry point.
type Reviewer struct {
	skillPerms SkillPermissions
	review     ReviewFunc
	ctx        context.Context

	mu       sync.Mutex
	inFlight bool
}

// NewSessionReviewer builds a Reviewer from cfg's arm config. review is invoked
// on its own goroutine when the model requests a review (via the ReviewSkill
// tool); disabled arms never fire. ctx bounds pending settlement waits to the
// session lifetime.
func NewSessionReviewer(ctx context.Context, cfg Config, review ReviewFunc) *Reviewer {
	return &Reviewer{
		ctx:        ctx,
		skillPerms: cfg.Skills,
		review:     review,
	}
}

// Observe processes one completed turn. Triggering is model-decided: a review
// fires only when the main agent called the ReviewSkill tool this turn
// (evolveRequested). The pass is scoped by skillUsed (a skill-use turn weighs update/delete of that
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

	if !skillPerms.Any() {
		return
	}

	r.mu.Lock()
	if r.inFlight {
		r.mu.Unlock()
		log.Logger().Warn("autoskill: skipping review, a prior review is still running")
		return
	}
	r.inFlight = true
	r.mu.Unlock()

	// The live slice can be reused after Observe returns; message contents are immutable.
	snapshot := slices.Clone(latestExchange(result.Messages))
	finalization := result.Finalization

	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Logger().Warn("autoskill: review panicked (recovered)",
					zap.Any("panic", rec),
					zap.Stack("stack"),
				)
			}
			r.mu.Lock()
			r.inFlight = false
			r.mu.Unlock()
		}()
		if finalization != nil {
			ctx, cancel := context.WithTimeout(r.ctx, time.Minute)
			defer cancel()
			if err := finalization.Wait(ctx); err != nil && !errors.Is(err, core.ErrTurnCompleted) {
				return
			}
		}
		if r.review != nil && r.ctx.Err() == nil {
			r.review(skillPerms, snapshot)
		}
	}()
}
