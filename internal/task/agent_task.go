package task

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"time"
)

// AgentTask represents a background agent task
// It implements the BackgroundTask interface
type AgentTask struct {
	parentSessionID string // fixed before registration

	ID          string     // Unique task ID
	AgentName   string     // Exact optional agent name
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

	mu             sync.RWMutex // Protects mutable fields
	attempt        int
	attempts       []AttemptInfo
	attemptStart   time.Time
	inspectionFile string // Native transcript fallback; never receives task-log records.
	finishing      bool
	attemptOutput  bytes.Buffer
	launchState    any
	permissionMode string           // Effective mode resolved before the worker starts.
	progress       string           // Latest non-metadata activity for the live tracker.
	outputErr      string           // First output-log failure; a nonempty path alone is not proof of durability.
	liveView       string           // Current structured progress, if the caller supplies one.
	liveRender     func(int) string // Optional width-aware live renderer.
	done           chan struct{}    // Closed when task completes
}

// Verify AgentTask implements BackgroundTask
var _ BackgroundTask = (*AgentTask)(nil)

// NewAgentTask creates a new agent task; outputPath "" keeps output in memory.
func NewAgentTask(id, agentName, description string, ctx context.Context, cancel context.CancelFunc, outputPath string) *AgentTask {
	outputFile, outputErr := initOutputFile(outputPath)
	task := &AgentTask{
		attempt:      1,
		attemptStart: time.Now(),
		ID:           id,
		AgentName:    agentName,
		Description:  description,
		Status:       StatusRunning,
		StartTime:    time.Now(),
		OutputFile:   outputFile,
		ctx:          ctx,
		cancel:       cancel,
		done:         make(chan struct{}),
	}
	if outputErr != nil {
		task.outputErr = outputErr.Error()
	}
	task.recordOutput(task.OutputFile, outputRecord{
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
func (t *AgentTask) SetIdentity(agentName, sessionID string) {
	t.mu.Lock()
	changed := false
	if t.AgentName != agentName {
		t.AgentName = agentName
		changed = true
	}
	if sessionID != "" {
		t.SessionID = sessionID
		changed = true
	}
	outputFile := t.OutputFile
	t.mu.Unlock()

	if changed {
		t.recordOutput(outputFile, outputRecord{
			Event: "agent.identity",
			Metadata: map[string]any{
				"agent_name": agentName,
				"agent_id":   sessionID,
			},
		})
	}
}

// SetPermissionMode records the already-resolved policy before execution starts.
func (t *AgentTask) SetPermissionMode(mode string) {
	t.mu.Lock()
	t.permissionMode = mode
	outputFile := t.OutputFile
	t.mu.Unlock()
	t.recordOutput(outputFile, outputRecord{
		Event:    "agent.permissions",
		Metadata: map[string]any{"mode": mode},
	})
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
	return t.inspectionPathLocked()
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
	appendCapped(&t.attemptOutput, data)
	outputFile := t.OutputFile
	t.mu.Unlock()

	t.recordOutput(outputFile, outputRecord{
		Event:   "task.output",
		Content: string(data),
	})
}

// AppendProgress appends a progress message
func (t *AgentTask) AppendProgress(msg string) {
	msg = strings.TrimSpace(msg)
	t.mu.Lock()
	if msg != "" && !strings.HasPrefix(msg, "Model: ") && !strings.HasPrefix(msg, "Mode: ") && !strings.HasPrefix(msg, "Usage: ") {
		// The tracker needs the latest action, not a token line that replaces it.
		// Tool descriptions are already bounded by the executor.
		t.progress = msg
	}
	outputFile := t.OutputFile
	t.mu.Unlock()

	t.recordOutput(outputFile, outputRecord{
		Event:   "task.progress",
		Content: msg,
	})
}

func (t *AgentTask) recordOutput(path string, record outputRecord) {
	if err := appendOutputFile(path, record); err != nil {
		t.mu.Lock()
		if t.outputErr == "" {
			t.outputErr = err.Error()
		}
		t.mu.Unlock()
	}
}

// SetLiveView replaces the task's compact, in-memory progress display.
// The task log still records individual progress events for later inspection.
func (t *AgentTask) SetLiveView(view string) {
	t.mu.Lock()
	t.liveView = view
	t.mu.Unlock()
}

// SetLiveViewRenderer lets a structured task reflow its live view when the
// terminal width changes. The caller owns synchronization of renderer state.
func (t *AgentTask) SetLiveViewRenderer(render func(int) string) {
	t.mu.Lock()
	t.liveRender = render
	t.mu.Unlock()
}

// RenderLiveView returns a width-aware view when supplied, otherwise the last
// snapshot. It never calls the renderer while holding the task lock.
func (t *AgentTask) RenderLiveView(width int) string {
	t.mu.RLock()
	render, snapshot := t.liveRender, t.liveView
	t.mu.RUnlock()
	if render != nil {
		return render(width)
	}
	return snapshot
}

// GetOutput returns the current output
func (t *AgentTask) GetOutput() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.attemptOutput.String()
}

// Complete marks the task as completed and notifies subscribers.
// A context.Canceled error records the task as stopped (deliberate cancel),
// any other error as failed. It is idempotent — a second call is a no-op.
func (t *AgentTask) Complete(err error) {
	t.CompleteAttempt(0, err)
}

// markKilled marks the task as killed (internal use).
func (t *AgentTask) markKilled() { t.finalize(StatusKilled, "") }

// finalize is AgentTask's single terminal transition; see BashTask.finalize for
// the invariant it enforces and why.
func (t *AgentTask) finalize(status TaskStatus, errText string) {
	t.finalizeAttempt(0, status, errText)
}

func (t *AgentTask) finalizeAttempt(generation int, status TaskStatus, errText string) {
	t.mu.Lock()
	if t.Status != StatusRunning || (generation != 0 && generation != t.attempt) {
		t.mu.Unlock()
		return
	}
	generation = t.attempt
	t.Status = status
	t.Error = errText
	t.EndTime = time.Now()
	t.attempts = append(t.attempts, AttemptInfo{Attempt: t.attempt, Status: status, StartTime: t.attemptStart, EndTime: t.EndTime, Error: errText, Output: t.attemptOutput.String()})
	// Retain every attempt's metadata, but bound aggregate snapshot output.
	remaining := maxOutputBufferSize
	for i := len(t.attempts) - 1; i >= 0; i-- {
		output := t.attempts[i].Output
		if len(output) > remaining {
			output = strings.Clone(output[len(output)-remaining:])
		}
		t.attempts[i].Output = output
		remaining -= len(output)
	}
	done := t.done
	t.finishing = true
	outputFile := t.OutputFile
	t.mu.Unlock()

	// File I/O and done channel outside lock
	t.recordOutput(outputFile, outputRecord{
		Event:  "task.completed",
		Status: string(status),
		Metadata: map[string]any{
			"attempt": generation,
			"error":   errText,
		},
	})

	notifyTaskCompleted(t.GetStatus())
	t.mu.Lock()
	t.finishing = false
	close(done)
	t.mu.Unlock()
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
	t.mu.RLock()
	done := t.done
	t.mu.RUnlock()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// Stop gracefully stops the task by canceling the context
func (t *AgentTask) Stop() error {
	return t.StopAttempt(t.GetStatus().Attempt)
}

// Kill forcefully terminates the task
func (t *AgentTask) Kill() error {
	info := t.GetStatus()
	if info.Status != StatusRunning {
		return nil
	}
	return t.KillAttempt(info.Attempt)
}

// GetStatus returns the current task status info
func (t *AgentTask) GetStatus() TaskInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return TaskInfo{
		Attempt:         t.attempt,
		Attempts:        append([]AttemptInfo(nil), t.attempts...),
		ParentSessionID: t.parentSessionID,

		ID:             t.ID,
		Type:           TaskTypeAgent,
		Description:    t.Description,
		Status:         t.Status,
		StartTime:      t.StartTime,
		EndTime:        t.EndTime,
		Error:          t.Error,
		Output:         t.attemptOutput.String(),
		OutputFile:     t.inspectionPathLocked(),
		OutputError:    t.outputErr,
		Progress:       t.progress,
		AgentName:      t.AgentName,
		PermissionMode: t.permissionMode,
		AgentSessionID: t.SessionID,
		StepCount:      t.StepCount,
		TokenUsage:     t.TokenUsage,
		LiveView:       t.liveView,
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
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.ctx
}
