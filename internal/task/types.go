package task

import (
	"time"
)

// TaskType represents the type of background task
type TaskType string

const (
	TaskTypeBash  TaskType = "bash"
	TaskTypeAgent TaskType = "agent"
)

// TaskStatus represents the status of a background task
type TaskStatus string

const (
	StatusRunning   TaskStatus = "running"
	StatusCompleted TaskStatus = "completed"
	StatusFailed    TaskStatus = "failed"
	StatusKilled    TaskStatus = "killed"
	// StatusStopped marks a task whose run was cancelled (user stop, shutdown)
	// rather than failing on its own. Kept distinct from StatusFailed so the
	// main agent does not treat a deliberate stop as an error to retry.
	StatusStopped TaskStatus = "stopped"
)

// BackgroundTask is the common interface for all background task types
// Both BashTask and AgentTask implement this interface
//
// An implementation owes one thing the compiler cannot check: a Stop-induced
// exit must stay distinguishable from a failure, so that a task the user
// called off is not reported as broken work to retry. How is left open,
// because the two existing types genuinely differ — a bash child dies of an
// opaque signal and so must record the intent up front, while an agent
// goroutine returns context.Canceled, which says it already.
type BackgroundTask interface {
	// GetID returns the unique task identifier
	GetID() string

	// GetType returns the task type (bash or agent)
	GetType() TaskType

	// GetDescription returns a brief description of the task
	GetDescription() string

	// GetStatus returns the current task status info
	GetStatus() TaskInfo

	// IsRunning returns true if the task is still running
	IsRunning() bool

	// WaitForCompletion waits until the task completes or timeout
	// Returns true if completed, false if timeout
	WaitForCompletion(timeout time.Duration) bool

	// Stop asks the task to exit on its own and returns without waiting for
	// it to. Escalation is the caller's job, not the implementer's:
	// Manager.Kill waits gracefulStopTimeout and then calls Kill, so a Stop
	// must not enforce a deadline of its own.
	//
	// An implementation must not race ahead of whatever hard-kill it is wired
	// to. BashTask.Stop signals rather than cancelling its context precisely
	// because exec fires cmd.Cancel — a SIGKILL — the moment that context is
	// done, which would land before the SIGTERM and make the graceful stop a
	// lie. Cancelling is right for AgentTask only because nothing else is
	// listening for it.
	Stop() error

	// Kill forcefully terminates the task (SIGKILL for bash). It is safe to
	// call after Stop, and after the task has already ended.
	Kill() error

	// AppendOutput appends data to the output buffer
	AppendOutput(data []byte)

	// GetOutput returns the current output
	GetOutput() string
}

// TaskInfo is a snapshot of task information
// It contains both common fields and type-specific fields
type TaskInfo struct {
	// Common fields
	ID          string
	Type        TaskType
	Description string
	Status      TaskStatus
	StartTime   time.Time
	EndTime     time.Time
	Output      string
	OutputFile  string
	Error       string

	// Bash-specific fields
	Command  string
	PID      int
	ExitCode int

	// Agent-specific fields
	AgentType      string
	AgentName      string
	AgentSessionID string
	StepCount      int
	TokenUsage     int
}
