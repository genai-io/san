package task

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// SetInspectionFile records a native transcript without treating it as a task log.
func (t *AgentTask) SetInspectionFile(path string) {
	t.mu.Lock()
	t.inspectionFile = path
	t.mu.Unlock()
}
func (t *AgentTask) inspectionPathLocked() string {
	if path := usableOutputPath(t.OutputFile, t.outputErr); path != "" {
		return path
	}
	return t.inspectionFile
}

// LaunchState is process-local continuation configuration, not crash recovery.
func (t *AgentTask) LaunchState() any     { t.mu.RLock(); defer t.mu.RUnlock(); return t.launchState }
func (t *AgentTask) SetLaunchState(v any) { t.mu.Lock(); t.launchState = v; t.mu.Unlock() }

// CompleteAttempt ignores callbacks from retired executions.
// Zero selects the current attempt for unscoped Complete callers.
func (t *AgentTask) CompleteAttempt(n int, err error) {
	status, text := StatusCompleted, ""
	if errors.Is(err, context.Canceled) {
		status, text = StatusStopped, "stopped before completion"
	} else if err != nil {
		status, text = StatusFailed, err.Error()
	}
	t.finalizeAttempt(n, status, text)
}

// StopAttempt cancels only the execution selected by the caller's snapshot.
func (t *AgentTask) StopAttempt(n int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if n != t.attempt {
		return fmt.Errorf("attempt %d is no longer current", n)
	}
	t.stopRequested = true
	if t.startCancel != nil {
		t.startCancel()
	}
	if t.Status != StatusRunning {
		if t.Status != StatusKilled {
			t.Status = StatusStopped
		}
		return nil
	}
	if t.cancel != nil {
		t.cancel()
	}
	return nil
}

// KillAttempt retires only this generation. Killed tasks cannot be continued,
// even if an uncooperative tool has not yet returned from cancellation.
func (t *AgentTask) KillAttempt(n int) error {
	if err := t.StopAttempt(n); err != nil {
		return err
	}
	t.finalizeAttempt(n, StatusKilled, "")
	return nil
}

// WaitForAttemptCompletion never accidentally waits for a newer execution.
// Already retired generations are complete; future generations are invalid.
func (t *AgentTask) WaitForAttemptCompletion(n int, timeout time.Duration) bool {
	t.mu.RLock()
	if n != t.attempt {
		retired := n > 0 && n < t.attempt
		t.mu.RUnlock()
		return retired
	}
	done := t.done
	t.mu.RUnlock()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// RecoveryBudgetAvailable permits one corrective execution after a failure.
// Successful follow-ups reset the chain. Exhaustion requires a new plan, rather
// than silently turning completion notifications into an infinite retry loop.
func RecoveryBudgetAvailable(attempts []AttemptInfo) bool {
	failures := 0
	for i := len(attempts) - 1; i >= 0 && attempts[i].Status == StatusFailed; i-- {
		failures++
	}
	return failures < 2
}

// BeginAttempt atomically rejects overlapping and deliberately terminated work.
func (t *AgentTask) BeginAttempt(parent string, ctx context.Context, cancel context.CancelFunc) (int, error) {
	return t.BeginAttemptAfter(0, parent, ctx, cancel)
}

// BeginAttemptAfter fences approval/preflight against a newer execution.
// Zero retains the current-task behavior for internal callers.
func (t *AgentTask) BeginAttemptAfter(previous int, parent string, ctx context.Context, cancel context.CancelFunc) (int, error) {
	return t.StartAttemptAfter(previous, parent, ctx, cancel, nil, nil)
}

// StartAttemptAfter checks eligibility before setup without counting an attempt.
// prepare runs outside the task lock and can be canceled by Stop. accept persists
// input under the lock before publication; it must not call this task's methods.
func (t *AgentTask) StartAttemptAfter(previous int, parent string, ctx context.Context, cancel context.CancelFunc, prepare, accept func() error) (int, error) {
	t.mu.Lock()
	if t.starting {
		t.mu.Unlock()
		return 0, fmt.Errorf("a continuation is already starting")
	}
	if err := t.checkAttemptLocked(previous, parent); err != nil {
		t.mu.Unlock()
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		t.mu.Unlock()
		return 0, err
	}
	t.starting, t.startCancel = true, cancel
	previous = t.attempt
	t.mu.Unlock()
	starting := true
	defer func() {
		if starting {
			t.mu.Lock()
			t.starting, t.startCancel = false, nil
			t.mu.Unlock()
		}
	}()
	if prepare != nil {
		if err := prepare(); err != nil {
			return 0, err
		}
	}
	t.mu.Lock()
	if err := t.checkAttemptLocked(previous, parent); err != nil {
		t.mu.Unlock()
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		t.mu.Unlock()
		return 0, err
	}
	if accept != nil {
		if err := accept(); err != nil {
			t.mu.Unlock()
			return 0, err
		}
	}
	// Once acceptance succeeds, a concurrent Stop cancels admitted work rather
	// than turning persisted input into a rejected continuation.
	t.starting, t.startCancel, starting = false, nil, false
	t.foreground = false
	t.attempt++
	n := t.attempt
	t.Status = StatusRunning
	t.Error = ""
	t.EndTime = time.Time{}
	t.attemptStart = time.Now()
	t.attemptOutput.Reset()
	t.sourceMessageSeq = 0
	t.StepCount, t.TokenUsage = 0, 0
	t.progress, t.liveView, t.liveRender = "", "", nil
	t.ctx, t.cancel = ctx, cancel
	t.done = make(chan struct{})
	t.mu.Unlock()
	notifyTaskCreated(t.GetStatus())
	return n, nil
}

func (t *AgentTask) checkAttemptLocked(previous int, parent string) error {
	if previous > 0 && previous != t.attempt {
		return fmt.Errorf("selected execution changed before continuation")
	}
	if t.finishing || parent != t.parentSessionID || t.Status == StatusRunning || t.Status == StatusStopped || t.Status == StatusKilled {
		return fmt.Errorf("task cannot be continued: wrong owner, running, or deliberately stopped")
	}
	if !RecoveryBudgetAvailable(t.attempts) {
		return fmt.Errorf("recovery budget exhausted after two consecutive failed executions; report the blocker and choose a new plan")
	}
	return nil
}
