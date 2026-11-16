package workflow

import "time"

// Checkpoint is the committed state of a node. Only successful results are
// reusable; skipped and omitted nodes are recalculated from their upstreams.
type Checkpoint struct {
	Status     Status             `json:"status"`
	Output     string             `json:"output,omitempty"`
	Error      string             `json:"error,omitempty"`
	StartedAt  time.Time          `json:"started_at,omitempty"`
	FinishedAt time.Time          `json:"finished_at,omitempty"`
	Duration   time.Duration      `json:"duration,omitempty"`
	Attempt    int                `json:"attempt,omitempty"`
	History    []Attempt          `json:"history,omitempty"`
	PlanHash   string             `json:"plan_hash,omitempty"`
	Workers    []WorkerCheckpoint `json:"workers,omitempty"`
}

// Attempt retains the outcome of an earlier execution without treating it as
// the current result.
type Attempt struct {
	Number     int           `json:"number"`
	Status     Status        `json:"status"`
	Output     string        `json:"output,omitempty"`
	Error      string        `json:"error,omitempty"`
	StartedAt  time.Time     `json:"started_at,omitempty"`
	FinishedAt time.Time     `json:"finished_at,omitempty"`
	Duration   time.Duration `json:"duration,omitempty"`
}

// Worker identity is its position in a fixed plan, never its display label.
type WorkerCheckpoint struct {
	Index int        `json:"index"`
	Label string     `json:"label"`
	State Checkpoint `json:"state"`
}

// RunRecord keeps a definition and inputs fixed across execution attempts.
// Activity is a bounded live tail; complete outputs remain in checkpoints.
type RunRecord struct {
	ID         string                `json:"id"`
	Name       string                `json:"name"`
	Definition string                `json:"definition"`
	Inputs     map[string]string     `json:"inputs,omitempty"`
	Status     string                `json:"status"`
	TaskID     string                `json:"task_id,omitempty"`
	StartedAt  time.Time             `json:"started_at"`
	UpdatedAt  time.Time             `json:"updated_at"`
	FinishedAt time.Time             `json:"finished_at,omitempty"`
	Nodes      map[string]Checkpoint `json:"nodes"`
	Activity   map[string][]string   `json:"activity,omitempty"`
	Error      string                `json:"error,omitempty"`
	Output     string                `json:"output,omitempty"`
}

const (
	RunRunning     = "running"
	RunSucceeded   = "succeeded"
	RunFailed      = "failed"
	RunStopped     = "stopped"
	RunInterrupted = "interrupted"
)
