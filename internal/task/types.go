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

	// Stop gracefully stops the task (SIGTERM for bash, context cancel for agent)
	Stop() error

	// Kill forcefully terminates the task (SIGKILL for bash)
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
