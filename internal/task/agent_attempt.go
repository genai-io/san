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
	return t.BeginAttemptWithSetup(ctx, cancel, 0, parent, AttemptSetup{})
}

// AttemptSetup is the work a continuation does between its eligibility check
// and publication. Prepare runs outside the task lock and is canceled by Stop.
// Accept runs under the task lock, so Stop cannot interleave between accepting
// input and publishing the attempt; keep it to one bounded commit, and never
// call this task's methods or deliver queued mail from it.
type AttemptSetup struct {
	Prepare func() error
	Accept  func() error
}

// BeginAttemptWithSetup runs setup between eligibility and publication. A
// nonzero previous fences approval/preflight against a newer execution; zero
// accepts the current one. Only one start runs at a time; a rejected start
// consumes no attempt or recovery budget.
func (t *AgentTask) BeginAttemptWithSetup(ctx context.Context, cancel context.CancelFunc, previous int, parent string, setup AttemptSetup) (int, error) {
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
	observed := t.attempt // Fence setup even when the caller passed zero.
	t.mu.Unlock()
	ownsStart := true
	defer func() {
		if ownsStart {
			t.mu.Lock()
			t.starting, t.startCancel = false, nil
			t.mu.Unlock()
		}
	}()
	if setup.Prepare != nil {
		if err := setup.Prepare(); err != nil {
			return 0, err
		}
	}
	t.mu.Lock()
	if err := t.checkAttemptLocked(observed, parent); err != nil {
		t.mu.Unlock()
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		t.mu.Unlock()
		return 0, err
	}
	if setup.Accept != nil {
		if err := setup.Accept(); err != nil {
			t.mu.Unlock()
			return 0, err
		}
	}
	// Once acceptance succeeds, a concurrent Stop cancels admitted work rather
	// than turning persisted input into a rejected continuation.
	t.starting, t.startCancel, ownsStart = false, nil, false
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
