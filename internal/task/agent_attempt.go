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
	t.mu.RLock()
	defer t.mu.RUnlock()
	if n != t.attempt || t.Status != StatusRunning {
		return fmt.Errorf("attempt %d is no longer running", n)
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

// BeginAttemptAfter also fences approval/preflight against a newer execution.
// Zero retains the legacy current-task behavior for internal callers.
func (t *AgentTask) BeginAttemptAfter(previous int, parent string, ctx context.Context, cancel context.CancelFunc) (int, error) {
	t.mu.Lock()
	if previous > 0 && previous != t.attempt {
		t.mu.Unlock()
		return 0, fmt.Errorf("selected execution changed before continuation")
	}
	if t.finishing || parent != t.parentSessionID || t.Status == StatusRunning || t.Status == StatusStopped || t.Status == StatusKilled {
		t.mu.Unlock()
		return 0, fmt.Errorf("task cannot be continued: wrong owner, running, or deliberately stopped")
	}
	if !RecoveryBudgetAvailable(t.attempts) {
		t.mu.Unlock()
		return 0, fmt.Errorf("recovery budget exhausted after two consecutive failed executions; report the blocker and choose a new plan")
	}
	t.attempt++
	n := t.attempt
	t.Status = StatusRunning
	t.Error = ""
	t.EndTime = time.Time{}
	t.attemptStart = time.Now()
	t.attemptOutput.Reset()
	t.StepCount, t.TokenUsage = 0, 0
	t.progress = ""
	t.liveView = ""
	t.liveRender = nil
	t.ctx = ctx
	t.cancel = cancel
	t.done = make(chan struct{})
	t.mu.Unlock()
	notifyTaskCreated(t.GetStatus())
	return n, nil
}
