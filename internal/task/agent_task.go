package task

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"time"
)

// AgentTask represents a background agent task
// It implements the BackgroundTask interface
type AgentTask struct {
	ID          string     // Unique task ID
	AgentType   string     // Fixed protocol identity ("subagent")
	AgentName   string     // Display name for this run (Explorer, Editor, etc.)
	Description string     // Brief description of the task
	Status      TaskStatus // Current status
	StartTime   time.Time  // When the task started
	EndTime     time.Time  // When the task ended (if completed)
	SessionID   string     // Resumable session/agent ID
	OutputFile  string     // Transcript/output path when available
	StepCount   int        // Number of LLM inference steps
	TokenUsage  int        // Total tokens consumed
	Error       string     // Error message (if failed)

	ctx    context.Context    // Task context
	cancel context.CancelFunc // Cancel function

	mu       sync.RWMutex  // Protects mutable fields
	output   bytes.Buffer  // Collected output from the agent
	done     chan struct{} // Closed when task completes
	doneOnce sync.Once     // Guards done channel close
}

// Verify AgentTask implements BackgroundTask
var _ BackgroundTask = (*AgentTask)(nil)

// NewAgentTask creates a new agent task
func NewAgentTask(id, agentName, description string, ctx context.Context, cancel context.CancelFunc) *AgentTask {
	task := &AgentTask{
		ID:          id,
		AgentName:   agentName,
		Description: description,
		Status:      StatusRunning,
		StartTime:   time.Now(),
		OutputFile:  initOutputFile(id),
		ctx:         ctx,
		cancel:      cancel,
		done:        make(chan struct{}),
	}
	appendOutputFile(task.OutputFile, outputRecord{
		Event:       "task.started",
		TaskType:    string(TaskTypeAgent),
		Description: description,
		Metadata: map[string]any{
			"agent_name": agentName,
		},
	})
	return task
}

// SetIdentity stores stable agent identity metadata for continuation.
func (t *AgentTask) SetIdentity(agentType, sessionID string) {
	t.mu.Lock()
	changed := false
	if agentType != "" {
		t.AgentType = agentType
		changed = true
	}
	if sessionID != "" {
		t.SessionID = sessionID
		changed = true
	}
	outputFile := t.OutputFile
	t.mu.Unlock()

	if changed {
		appendOutputFile(outputFile, outputRecord{
			Event: "agent.identity",
			Metadata: map[string]any{
				"agent_type": agentType,
				"agent_id":   sessionID,
			},
		})
	}
}

// SetOutputFile stores the stable transcript/output path for later inspection.
func (t *AgentTask) SetOutputFile(path string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if path != "" && t.OutputFile == "" {
		t.OutputFile = path
	}
}

// GetOutputFile returns the transcript/output path, safe for concurrent use.
func (t *AgentTask) GetOutputFile() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.OutputFile
}

// GetID returns the unique task identifier
func (t *AgentTask) GetID() string {
	return t.ID
}

// GetType returns the task type
func (t *AgentTask) GetType() TaskType {
	return TaskTypeAgent
}

// GetDescription returns the task description
func (t *AgentTask) GetDescription() string {
	return t.Description
}

// AppendOutput appends data to the output buffer
func (t *AgentTask) AppendOutput(data []byte) {
	t.mu.Lock()
	appendCapped(&t.output, data)
	outputFile := t.OutputFile
	t.mu.Unlock()

	appendOutputFile(outputFile, outputRecord{
		Event:   "task.output",
		Content: string(data),
	})
}

// AppendProgress appends a progress message
func (t *AgentTask) AppendProgress(msg string) {
	t.mu.Lock()
	outputFile := t.OutputFile
	t.mu.Unlock()

	appendOutputFile(outputFile, outputRecord{
		Event:   "task.progress",
		Content: msg,
	})
}

// GetOutput returns the current output
func (t *AgentTask) GetOutput() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.output.String()
}

// Complete marks the task as completed and notifies subscribers.
// A context.Canceled error records the task as stopped (deliberate cancel),
// any other error as failed. It is idempotent — a second call is a no-op.
func (t *AgentTask) Complete(err error) {
	switch {
	case err == nil:
		t.finalize(StatusCompleted, "")
	case errors.Is(err, context.Canceled):
		t.finalize(StatusStopped, "stopped before completion")
	default:
		t.finalize(StatusFailed, err.Error())
	}
}

// markKilled marks the task as killed (internal use).
func (t *AgentTask) markKilled() { t.finalize(StatusKilled, "") }

// finalize is AgentTask's single terminal transition; see BashTask.finalize for
// the invariant it enforces and why.
func (t *AgentTask) finalize(status TaskStatus, errText string) {
	t.mu.Lock()
	if t.Status != StatusRunning {
		t.mu.Unlock()
		return
	}
	t.Status = status
	t.Error = errText
	t.EndTime = time.Now()
	outputFile := t.OutputFile
	t.mu.Unlock()

	// File I/O and done channel outside lock
	appendOutputFile(outputFile, outputRecord{
		Event:  "task.completed",
		Status: string(status),
		Metadata: map[string]any{
			"error": errText,
		},
	})

	t.doneOnce.Do(func() { close(t.done) })
	notifyTaskCompleted(t.GetStatus())
}

// IsRunning returns true if the task is still running
func (t *AgentTask) IsRunning() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.Status == StatusRunning
}

// WaitForCompletion waits until the task completes or timeout.
// Returns true if completed, false if timeout.
func (t *AgentTask) WaitForCompletion(timeout time.Duration) bool {
	select {
	case <-t.done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// Stop gracefully stops the task by canceling the context
func (t *AgentTask) Stop() error {
	if t.cancel != nil {
		t.cancel()
	}
	return nil
}

// Kill forcefully terminates the task
func (t *AgentTask) Kill() error {
	if t.cancel != nil {
		t.cancel()
	}
	t.markKilled()
	return nil
}

// GetStatus returns the current task status info
func (t *AgentTask) GetStatus() TaskInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return TaskInfo{
		ID:             t.ID,
		Type:           TaskTypeAgent,
		Description:    t.Description,
		Status:         t.Status,
		StartTime:      t.StartTime,
		EndTime:        t.EndTime,
		Error:          t.Error,
		Output:         t.output.String(),
		OutputFile:     t.OutputFile,
		AgentType:      t.AgentType,
		AgentName:      t.AgentName,
		AgentSessionID: t.SessionID,
		StepCount:      t.StepCount,
		TokenUsage:     t.TokenUsage,
	}
}

// UpdateProgress updates the step count and token usage
func (t *AgentTask) UpdateProgress(roundCount, tokenUsage int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.StepCount = roundCount
	t.TokenUsage = tokenUsage
}

// GetContext returns the task's context
func (t *AgentTask) GetContext() context.Context {
	return t.ctx
}
