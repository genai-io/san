package task

import (
	"bytes"
	"context"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/genai-io/san/internal/proc"
)

// BashTask represents a background bash command task
// It implements the BackgroundTask interface
type BashTask struct {
	ID          string          // Unique task ID
	Command     string          // The command being executed
	Description string          // Brief description
	PID         int             // Process ID
	StartTime   time.Time       // When the task started
	OutputFile  string          // Stable output file path when available
	cmd         *exec.Cmd       // The running command
	ctx         context.Context // Task context

	cancel context.CancelFunc // Cancel function

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
func NewBashTask(id, command, description string, cmd *exec.Cmd, ctx context.Context, cancel context.CancelFunc) *BashTask {
	task := &BashTask{
		ID:          id,
		Command:     command,
		Description: description,
		status:      StatusRunning,
		PID:         cmd.Process.Pid,
		StartTime:   time.Now(),
		OutputFile:  initOutputFile(id),
		cmd:         cmd,
		ctx:         ctx,
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
	t.output.Write(data)
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

// Complete marks the task as completed
func (t *BashTask) Complete(exitCode int, err error) {
	status := StatusCompleted
	errMsg := ""
	if err != nil {
		status, errMsg = StatusFailed, err.Error()
	} else if exitCode != 0 {
		status = StatusFailed
	}
	t.finalize(status, exitCode, errMsg)
}

// markKilled marks the task as killed (internal use). The exit code follows
// the shell convention of 128+signal for SIGKILL, so a reader of the output
// record can't mistake a killed task's code for a successful 0.
func (t *BashTask) markKilled() { t.finalize(StatusKilled, 128+int(syscall.SIGKILL), "") }

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

// Stop gracefully stops the task (SIGTERM on Unix; on Windows there is no
// signal-based graceful stop, so the underlying helper hard-kills the child).
func (t *BashTask) Stop() error {
	if t.cancel != nil {
		t.cancel()
	}
	return proc.TerminateGroup(t.cmd, syscall.SIGTERM)
}

// Kill forcefully terminates the task (SIGKILL). markKilled runs even if the
// kill returned an error, so a child that races us to exit (or a Windows
// TerminateProcess that surfaces a benign error) still leaves the task in
// StatusKilled with `done` closed, instead of stuck in StatusRunning.
func (t *BashTask) Kill() error {
	if t.cancel != nil {
		t.cancel()
	}
	err := proc.TerminateGroup(t.cmd, syscall.SIGKILL)
	t.markKilled()
	return err
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
