package task

import (
	"bytes"
	"context"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/genai-io/san/internal/proc"
)

// BashTask represents a background bash command task
// It implements the BackgroundTask interface
type BashTask struct {
	ID          string    // Unique task ID
	Command     string    // The command being executed
	Description string    // Brief description
	PID         int       // Process ID
	StartTime   time.Time // When the task started
	OutputFile  string    // Stable output file path when available
	cmd         *exec.Cmd // The running command

	cancel context.CancelFunc // Cancel function

	// stopRequested records that Stop asked the child to exit, so Complete can
	// tell a deliberate stop from a crash. Written by the caller of Stop and
	// read on the goroutine waiting for the child, so it is atomic rather than
	// mu-guarded: it shares no invariant with the fields below and is never
	// read together with them.
	stopRequested atomic.Bool

	// reaped is set once cmd.Wait has collected the child. After that the raw
	// PGID is no longer ours — the kernel may reissue it — so Stop/Kill refuse
	// to signal it. Set by the process owner (markReaped), read by the signaller;
	// atomic for the same reason as stopRequested.
	reaped atomic.Bool

	mu       sync.RWMutex  // Protects mutable fields below
	status   TaskStatus    // Current status
	endTime  time.Time     // When the task ended (if completed)
	exitCode int           // Exit code (if completed)
	errMsg   string        // Error message (if failed)
	output   bytes.Buffer  // Collected stdout/stderr
	done     chan struct{} // Closed when task completes
	doneOnce sync.Once     // Guards done channel close
}

// Verify BashTask implements BackgroundTask
var _ BackgroundTask = (*BashTask)(nil)

// NewBashTask creates a new bash task
func NewBashTask(id, command, description string, cmd *exec.Cmd, cancel context.CancelFunc) *BashTask {
	task := &BashTask{
		ID:          id,
		Command:     command,
		Description: description,
		status:      StatusRunning,
		PID:         cmd.Process.Pid,
		StartTime:   time.Now(),
		OutputFile:  initOutputFile(id),
		cmd:         cmd,
		cancel:      cancel,
		done:        make(chan struct{}),
	}
	appendOutputFile(task.OutputFile, outputRecord{
		Event:       "task.started",
		TaskType:    string(TaskTypeBash),
		Description: description,
		Metadata: map[string]any{
			"command": command,
			"pid":     task.PID,
		},
	})
	return task
}

// GetID returns the unique task identifier
func (t *BashTask) GetID() string {
	return t.ID
}

// GetType returns the task type
func (t *BashTask) GetType() TaskType {
	return TaskTypeBash
}

// GetDescription returns the task description
func (t *BashTask) GetDescription() string {
	return t.Description
}

// AppendOutput appends data to the output buffer
func (t *BashTask) AppendOutput(data []byte) {
	t.mu.Lock()
	appendCapped(&t.output, data)
	outputFile := t.OutputFile
	t.mu.Unlock()

	appendOutputFile(outputFile, outputRecord{
		Event:   "task.output",
		Content: string(data),
	})
}

// GetOutput returns the current output
func (t *BashTask) GetOutput() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.output.String()
}

// Complete records the terminal status of a bash task that has exited, the
// counterpart to AgentTask.Complete and classifying the same three outcomes.
//
// A stopped run reaches cmd.Wait as an ordinary signal death ("signal:
// terminated"), never as context.Canceled, so unlike the agent case the error
// alone cannot tell a deliberate stop from a genuine failure. Stop records
// that it asked, and that flag is the only thing that distinguishes them: a
// run killed by its own timeout sets nothing and stays a failure. Without this
// a user-requested TaskStop was recorded as failed, and the main agent could
// retry work the user had just cancelled — the outcome StatusStopped exists to
// prevent.
func (t *BashTask) Complete(exitCode int, err error) {
	status, errMsg := StatusCompleted, ""
	switch {
	case t.stopRequested.Load():
		status, errMsg = StatusStopped, "stopped before completion"
	case err != nil:
		status, errMsg = StatusFailed, err.Error()
	case exitCode != 0:
		status = StatusFailed
	}
	t.finalize(status, exitCode, errMsg)
}

// signalExitCode renders a death by signal the way a shell does, so a reader
// of the output record can't mistake a killed task's code for a successful 0.
func signalExitCode(sig syscall.Signal) int { return 128 + int(sig) }

// markKilled marks the task as killed (internal use).
func (t *BashTask) markKilled() { t.finalize(StatusKilled, signalExitCode(syscall.SIGKILL), "") }

// finalize performs the one and only terminal transition a BashTask can make.
// Every route out of StatusRunning — clean exit, non-zero exit, error, kill —
// funnels through here, so it is structurally impossible to add an exit path
// that forgets to close `done` or to notify the lifecycle handler. A dropped
// notification used to strand the task's tracker entry in in_progress forever.
//
// It is idempotent: a second call after the task has left StatusRunning is a
// no-op, which is what makes Kill-then-Complete races harmless.
func (t *BashTask) finalize(status TaskStatus, exitCode int, errMsg string) {
	t.mu.Lock()
	if t.status != StatusRunning {
		t.mu.Unlock()
		return
	}
	t.status = status
	t.endTime = time.Now()
	t.exitCode = exitCode
	t.errMsg = errMsg
	outputFile := t.OutputFile
	t.mu.Unlock()

	appendOutputFile(outputFile, outputRecord{
		Event:  "task.completed",
		Status: string(status),
		Metadata: map[string]any{
			"exit_code": exitCode,
			"error":     errMsg,
		},
	})
	t.doneOnce.Do(func() { close(t.done) })
	notifyTaskCompleted(t.GetStatus())
}

// IsRunning returns true if the task is still running
func (t *BashTask) IsRunning() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.status == StatusRunning
}

// WaitForCompletion waits until the task completes or timeout.
// Returns true if completed, false if timeout.
func (t *BashTask) WaitForCompletion(timeout time.Duration) bool {
	select {
	case <-t.done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// Stop asks the task to exit gracefully, sending SIGTERM to its process group
// so the child gets a chance to clean up. On Windows there is no signal-based
// graceful stop, so the helper hard-kills instead.
//
// It deliberately leaves the task context alone. exec wires cmd.Cancel to
// SIGKILL the group the instant that context is done, so cancelling here would
// deliver the kill before the SIGTERM and the graceful stop would never
// actually happen. Escalation belongs to the caller: Manager.Kill waits
// gracefulStopTimeout and then falls back to Kill.
func (t *BashTask) Stop() error {
	t.stopRequested.Store(true)
	if t.reaped.Load() {
		return nil // already gone; the PGID is no longer ours to signal
	}
	return proc.TerminateGroup(t.cmd, syscall.SIGTERM)
}

// Kill forcefully terminates the task (SIGKILL). markKilled runs even if the
// kill returned an error, so a child that races us to exit (or a Windows
// TerminateProcess that surfaces a benign error) still leaves the task in
// StatusKilled with `done` closed, instead of stuck in StatusRunning.
//
// Cancelling the context is the whole kill: exec wires cmd.Cancel to SIGKILL
// the group, and os/exec invokes it before reaping the child, so that path
// cannot signal a reissued PGID. The direct signal is only a fallback for a
// task created without a cancel func, and it too refuses once reaped.
func (t *BashTask) Kill() error {
	var err error
	if t.cancel != nil {
		t.cancel()
	} else if !t.reaped.Load() {
		err = proc.TerminateGroup(t.cmd, syscall.SIGKILL)
	}
	t.markKilled()
	return err
}

// MarkReaped records that cmd.Wait has collected the child, after which Stop and
// Kill must not signal the raw PGID. Called by the process owner (the goroutine
// that runs cmd.Wait) immediately after the wait returns.
//
// This narrows the reuse window; it does not close it. The wait4 that frees the
// PGID and this flag cannot be made atomic without holding a lock across a
// blocking Wait, so a signal racing in the instant between them still targets a
// PGID that may already be reissued — an inherent limit of raw-PGID signalling,
// where the common outcome is a harmless ESRCH. The cancel-driven path in Kill
// is the only fully race-free one.
func (t *BashTask) MarkReaped() {
	t.reaped.Store(true)
}

// GetStatus returns the current task status info
func (t *BashTask) GetStatus() TaskInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return TaskInfo{
		ID:          t.ID,
		Type:        TaskTypeBash,
		Command:     t.Command,
		Description: t.Description,
		Status:      t.status,
		PID:         t.PID,
		StartTime:   t.StartTime,
		EndTime:     t.endTime,
		ExitCode:    t.exitCode,
		OutputFile:  t.OutputFile,
		Error:       t.errMsg,
		Output:      t.output.String(),
	}
}
