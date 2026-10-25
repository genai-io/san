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
// because the two existing types genuinely differ — a bash child reports its
// graceful SIGTERM, while an agent goroutine returns context.Canceled.
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
	// An implementation must not let its own hard-kill path pre-empt the
	// graceful attempt it is making here — a Stop that trips the very
	// mechanism that kills the task outright has not stopped it gracefully at
	// all, it has just taken a longer route to Kill.
	Stop() error

	// Kill forcefully terminates the task (SIGKILL for bash). It is safe to
	// call after Stop, and after the task has already ended.
	Kill() error

	// AppendOutput appends data to the output buffer
	AppendOutput(data []byte)

	// GetOutput returns the current output
	GetOutput() string
}

// FailureKind records observed runtime evidence, not a guess based on error prose.
type FailureKind string

const (
	FailureUnknown FailureKind = "unknown"
	FailureTimeout FailureKind = "timeout"
	FailureExit    FailureKind = "exit"
	FailureSignal  FailureKind = "signal"
	FailureAgent   FailureKind = "agent_error"
)

type FailureInfo struct {
	Kind    FailureKind   `json:"kind,omitempty"`
	Signal  string        `json:"signal,omitempty"`
	Timeout time.Duration `json:"timeout,omitempty"`
}

// Lifetime distinguishes finite jobs from explicitly session-owned services.
type Lifetime string

const (
	LifetimeJob     Lifetime = "job"
	LifetimeService Lifetime = "service"
)

// TaskInfo is a snapshot of task information
// It contains both common fields and type-specific fields
type AttemptInfo struct {
	Attempt            int
	Status             TaskStatus
	StartTime, EndTime time.Time
	Error, Output      string
}

type TaskInfo struct {
	Attempt         int
	Attempts        []AttemptInfo
	ParentSessionID string

	// Common fields
	ID          string
	Type        TaskType
	Description string
	Status      TaskStatus
	StartTime   time.Time
	EndTime     time.Time
	Output      string
	OutputFile  string
	OutputError string // First output-log failure, separate from execution outcome.
	Progress    string // Latest meaningful activity, separate from final model-facing output.
	Error       string
	Failure     FailureInfo
	Lifetime    Lifetime

	// Bash-specific fields
	Command  string
	PID      int
	ExitCode int

	// Agent-specific fields
	AgentName      string
	PermissionMode string // Effective launch-time mode for ordinary subagent tasks.
	AgentSessionID string
	StepCount      int
	TokenUsage     int
	LiveView       string // Current display for a running structured task, if any.
}
