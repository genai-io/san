package task

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
	"sync"
	"time"
)

// gracefulStopTimeout is how long a Stop'd task gets to exit on its own
// before Kill escalates. Named because BackgroundTask.Stop's contract points
// at it: implementers rely on this escalation instead of enforcing their own
// deadline.
const gracefulStopTimeout = 2 * time.Second

// Manager tracks background bash and subagent tasks.
//
// Nothing prunes tasks: they are kept for the life of the process, because
// TaskOutput resolves a task ID through this map and has no fallback to the
// output file on disk, so forgetting one would mean answering "task not
// found" for work the model ran minutes ago. What each task holds is bounded
// instead, by appendCapped.
type Manager struct {
	mu    sync.RWMutex
	tasks map[string]BackgroundTask
}

// NewManager creates a new *Manager.
func NewManager() *Manager {
	return &Manager{
		tasks: make(map[string]BackgroundTask),
	}
}

// CreateBashTask creates and registers a new bash task
func (m *Manager) CreateBashTask(cmd *exec.Cmd, command, description string, cancel context.CancelFunc) *BashTask {
	id := generateID()
	task := NewBashTask(id, command, description, cmd, cancel)

	m.mu.Lock()
	m.tasks[id] = task
	m.mu.Unlock()

	notifyTaskCreated(task.GetStatus())
	return task
}

// RegisterTask registers an existing task (used for agent tasks)
func (m *Manager) RegisterTask(task BackgroundTask) {
	m.mu.Lock()
	m.tasks[task.GetID()] = task
	m.mu.Unlock()

	notifyTaskCreated(task.GetStatus())
}

// generateID creates a short random ID
func generateID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// Get retrieves a task by ID
func (m *Manager) Get(id string) (BackgroundTask, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	task, ok := m.tasks[id]
	return task, ok
}

// getBashTask retrieves a bash task by ID (for backward compatibility)
func (m *Manager) getBashTask(id string) (*BashTask, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	task, ok := m.tasks[id]
	if !ok {
		return nil, false
	}
	bashTask, ok := task.(*BashTask)
	return bashTask, ok
}

// List returns all tasks
func (m *Manager) List() []BackgroundTask {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tasks := make([]BackgroundTask, 0, len(m.tasks))
	for _, t := range m.tasks {
		tasks = append(tasks, t)
	}
	return tasks
}

// ListRunning returns all running tasks
func (m *Manager) ListRunning() []BackgroundTask {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tasks := make([]BackgroundTask, 0)
	for _, t := range m.tasks {
		if t.IsRunning() {
			tasks = append(tasks, t)
		}
	}
	return tasks
}

// HasRunning reports whether any task is running. Callers that only need the
// answer should prefer it over len(ListRunning()), which allocates a slice of
// every running task to be measured and thrown away.
func (m *Manager) HasRunning() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, t := range m.tasks {
		if t.IsRunning() {
			return true
		}
	}
	return false
}

// IsRunning reports whether the task with this ID exists and is running.
func (m *Manager) IsRunning(id string) bool {
	t, ok := m.Get(id)
	return ok && t.IsRunning()
}

// Remove drops a task from the manager. Production code never calls it — see
// Manager for why tasks are otherwise kept forever. It exists so that tests
// sharing the process-global manager can put it back as they found it.
func (m *Manager) Remove(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.tasks, id)
}

// Kill terminates a task by ID
func (m *Manager) Kill(id string) error {
	m.mu.RLock()
	task, ok := m.tasks[id]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("task not found: %s", id)
	}

	if !task.IsRunning() {
		return fmt.Errorf("task already completed: %s", id)
	}

	// Try graceful stop first
	if err := task.Stop(); err != nil {
		// If stop fails, try kill
		return task.Kill()
	}

	// Wait for graceful exit with timeout
	timer := time.NewTimer(gracefulStopTimeout)
	defer timer.Stop()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if !task.IsRunning() {
				return nil
			}
		case <-timer.C:
			// Graceful stop timed out, force kill
			return task.Kill()
		}
	}
}
