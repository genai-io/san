package core

import (
	"context"
	"errors"
	"sync"
)

// ErrTurnCompleted means natural completion won the cancellation race; it makes
// no claim about normal-turn persistence (which may include asynchronous hooks).
var ErrTurnCompleted = errors.New("turn completed before cancellation")

// TurnFinalization identifies one exchange, not the current runtime turn.
// Acknowledge belongs to the single terminal-event consumer, after its required
// writes. Wait excludes painting, asynchronous hooks, and fsync durability.
type TurnFinalization struct {
	execution <-chan struct{}
	run       context.Context
	handled   chan struct{}
	settled   bool
	mu        sync.Mutex
	err       error
}

func newTurnFinalization(ctx context.Context, execution <-chan struct{}) *TurnFinalization {
	return &TurnFinalization{execution: execution, run: ctx, handled: make(chan struct{})}
}

// RecordError preserves producer-side persistence errors before event delivery.
func (f *TurnFinalization) RecordError(err error) {
	if f == nil || err == nil {
		return
	}
	f.mu.Lock()
	if !f.settled {
		f.err = errors.Join(f.err, err)
	}
	f.mu.Unlock()
}

// Acknowledge settles this receipt only; repeated acknowledgments are ignored.
func (f *TurnFinalization) Acknowledge(err error) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.settled {
		if f.err == nil {
			f.err = err
		} else if err != ErrTurnCompleted {
			f.err = errors.Join(f.err, err)
		}
		f.settled = true
		close(f.handled)
	}
}

// Wait requires both execution unwind and terminal handling. Missing delivery
// cannot succeed: shutdown or the caller's deadline returns an error instead.
func (f *TurnFinalization) Wait(ctx context.Context) error {
	if f == nil {
		return errors.New("no turn to finalize")
	}
	for _, done := range []<-chan struct{}{f.execution, f.handled} {
		select {
		case <-done:
			continue
		default:
		}
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		case <-f.run.Done():
			return errors.New("agent stopped before turn finalization")
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}
