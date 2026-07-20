package todo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Task represents a tracked task
type Task struct {
	ID              string         `json:"id"`
	Subject         string         `json:"subject"`
	Description     string         `json:"description"`
	ActiveForm      string         `json:"activeForm,omitempty"`
	Status          string         `json:"status"`
	Owner           string         `json:"owner,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	Blocks          []string       `json:"blocks"`
	BlockedBy       []string       `json:"blockedBy"`
	CreatedAt       time.Time      `json:"createdAt"`
	UpdatedAt       time.Time      `json:"updatedAt"`
	StatusChangedAt time.Time      `json:"statusChangedAt"` // when status last changed (for elapsed time display)
}

// Task status constants
const (
	StatusPending    = "pending"
	StatusInProgress = "in_progress"
	StatusCompleted  = "completed"
	StatusDeleted    = "deleted"
)

// Store is a thread-safe task store with optional disk persistence.
// When a storageDir is set, each task is persisted as {id}.json.
type Store struct {
	mu         sync.RWMutex
	tasks      map[string]*Task
	nextID     int
	storageDir string    // empty = in-memory only
	lastDirMod time.Time // last known dir mtime, for change detection in ReloadFromDisk
}

// NewStore creates a new in-memory Store
func NewStore() *Store {
	return &Store{
		tasks:  make(map[string]*Task),
		nextID: 1,
	}
}

// SetStorageDir sets the directory for disk persistence and loads existing tasks.
// If dir is empty, the store operates in memory-only mode.
func (s *Store) SetStorageDir(dir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.storageDir = dir
	if dir == "" {
		return nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create task storage dir: %w", err)
	}

	// Create lock file
	lockPath := filepath.Join(dir, ".lock")
	if _, err := os.Stat(lockPath); os.IsNotExist(err) {
		os.WriteFile(lockPath, nil, 0o644)
	}

	// Load existing tasks from disk, then reconcile: this is the moment a
	// fresh process adopts state written by a process that no longer exists.
	if err := s.loadFromDisk(); err != nil {
		return err
	}
	s.demoteOrphanedTasks()
	return nil
}

// demoteOrphanedTasks demotes tasks recorded as in_progress that no executor is
// advancing any more. Runs where a store is adopted — SetStorageDir and Import —
// so records written by a process that has since died stop claiming to be live,
// however that process ended. Must be called with s.mu held.
//
// Adoption also happens mid-session (resuming a session re-points the store), so
// liveness is asked of the task manager rather than assumed from the call site:
// a worker still executing keeps its status. Plan items have no executor to ask
// about and fall back to pending, the truthful state of work that was started
// but never finished.
//
// Not called from loadFromDisk — ReloadFromDisk uses that mid-session to pick up
// cross-process writes, where in_progress is expected and genuine.
func (s *Store) demoteOrphanedTasks() {
	for _, entry := range s.tasks {
		if entry.Status != StatusInProgress || WorkerRunning(entry) {
			continue
		}
		if BackgroundTaskID(entry) != "" {
			entry.Status = StatusCompleted
			if entry.Metadata == nil {
				entry.Metadata = map[string]any{}
			}
			entry.Metadata[metaStatusDetail] = StatusDetailInterrupted
		} else {
			entry.Status = StatusPending
		}
		entry.StatusChangedAt = time.Now()
		s.persistTask(entry)
	}
}

// loadFromDisk reads all {id}.json files from storageDir into memory.
// Must be called with s.mu held.
func (s *Store) loadFromDisk() error {
	entries, err := os.ReadDir(s.storageDir)
	if err != nil {
		return err
	}

	s.tasks = make(map[string]*Task)
	s.nextID = 1

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".json" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(s.storageDir, name))
		if err != nil {
			continue
		}

		var task Task
		if err := json.Unmarshal(data, &task); err != nil {
			continue
		}

		normalizeTaskSlices(&task)
		s.tasks[task.ID] = &task
		var idNum int
		if _, err := fmt.Sscanf(task.ID, "%d", &idNum); err == nil && idNum >= s.nextID {
			s.nextID = idNum + 1
		}
	}

	return nil
}

// persistTask writes a single task to disk. Must be called with s.mu held.
func (s *Store) persistTask(task *Task) {
	if s.storageDir == "" {
		return
	}
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "tracker: failed to marshal task %s: %v\n", task.ID, err)
		return
	}
	path := filepath.Join(s.storageDir, task.ID+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "tracker: failed to write task %s: %v\n", task.ID, err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		fmt.Fprintf(os.Stderr, "tracker: failed to rename task %s: %v\n", task.ID, err)
	}
}

// removeTaskFile deletes a task file from disk. Must be called with s.mu held.
func (s *Store) removeTaskFile(id string) {
	if s.storageDir == "" {
		return
	}
	_ = os.Remove(filepath.Join(s.storageDir, id+".json"))
}

// Create adds a new task and returns it
func (s *Store) Create(subject, description, activeForm string, metadata map[string]any) *Task {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := fmt.Sprintf("%d", s.nextID)
	s.nextID++

	now := time.Now()
	task := &Task{
		ID:              id,
		Subject:         subject,
		Description:     description,
		ActiveForm:      activeForm,
		Status:          StatusPending,
		Metadata:        metadata,
		Blocks:          []string{},
		BlockedBy:       []string{},
		CreatedAt:       now,
		UpdatedAt:       now,
		StatusChangedAt: now,
	}

	s.tasks[id] = task
	s.persistTask(task)
	return task
}

// Get retrieves a copy of a task by ID.
func (s *Store) Get(id string) (*Task, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	task, ok := s.tasks[id]
	if !ok || task.Status == StatusDeleted {
		return nil, false
	}
	cp := *task
	return &cp, true
}

// Update modifies an existing task. Returns error if task not found.
func (s *Store) Update(id string, opts ...UpdateOption) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.tasks[id]
	if !ok {
		return fmt.Errorf("task %s not found", id)
	}

	for _, opt := range opts {
		opt(task)
	}

	task.UpdatedAt = time.Now()
	s.persistTask(task)
	return nil
}

// List returns copies of all non-deleted tasks sorted by ID.
func (s *Store) List() []*Task {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tasks := make([]*Task, 0, len(s.tasks))
	for _, task := range s.tasks {
		if task.Status != StatusDeleted {
			cp := *task
			tasks = append(tasks, &cp)
		}
	}

	sort.Slice(tasks, func(i, j int) bool {
		return compareNumericIDs(tasks[i].ID, tasks[j].ID)
	})

	return tasks
}

// IsBlocked returns true if the task has any uncompleted blockers
func (s *Store) IsBlocked(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	task, ok := s.tasks[id]
	if !ok || task.Status == StatusDeleted {
		return false
	}

	for _, blockerID := range task.BlockedBy {
		blocker, ok := s.tasks[blockerID]
		if ok && blocker.Status != StatusCompleted && blocker.Status != StatusDeleted {
			return true
		}
	}
	return false
}

// OpenBlockers returns IDs of uncompleted tasks that block the given task
func (s *Store) OpenBlockers(id string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	task, ok := s.tasks[id]
	if !ok || task.Status == StatusDeleted {
		return nil
	}

	var open []string
	for _, blockerID := range task.BlockedBy {
		blocker, ok := s.tasks[blockerID]
		if ok && blocker.Status != StatusCompleted && blocker.Status != StatusDeleted {
			open = append(open, blockerID)
		}
	}
	return open
}

// Delete marks a task as deleted and removes its file from disk
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.tasks[id]
	if !ok {
		return fmt.Errorf("task %s not found", id)
	}

	task.Status = StatusDeleted
	task.UpdatedAt = time.Now()
	s.removeTaskFile(id)
	return nil
}

// AllMarkedCompleted reports whether the store has tasks and every one of them
// carries StatusCompleted.
//
// The question is what the list records about itself — whether the model closed
// out every item it wrote down — and Status is the only record of that.
// Deriving it from executors would answer a different question and answer it
// wrongly: a task left in_progress with no executor is abandoned work, not
// finished work, and marking it done would wipe the list at turn end and take
// the [stalled] row down with it.
func (s *Store) AllMarkedCompleted() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.tasks) == 0 {
		return false
	}
	for _, t := range s.tasks {
		if t.Status == StatusDeleted {
			continue
		}
		if t.Status != StatusCompleted {
			return false
		}
	}
	return true
}

// Reset clears all tasks (for new sessions)
func (s *Store) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Remove all task files from disk
	if s.storageDir != "" {
		for id := range s.tasks {
			_ = os.Remove(filepath.Join(s.storageDir, id+".json"))
		}
	}

	s.tasks = make(map[string]*Task)
	s.nextID = 1
}

// Export returns a snapshot of all tasks (including deleted) for session persistence
func (s *Store) Export() []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tasks := make([]Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		tasks = append(tasks, *t)
	}
	sort.Slice(tasks, func(i, j int) bool {
		return compareNumericIDs(tasks[i].ID, tasks[j].ID)
	})
	return tasks
}

// Import restores tasks from a snapshot (used when loading a session)
func (s *Store) Import(tasks []Task) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.tasks = make(map[string]*Task, len(tasks))
	s.nextID = 1
	for i := range tasks {
		t := tasks[i]
		normalizeTaskSlices(&t)
		s.tasks[t.ID] = &t
		var idNum int
		if _, err := fmt.Sscanf(t.ID, "%d", &idNum); err == nil && idNum >= s.nextID {
			s.nextID = idNum + 1
		}
		s.persistTask(&t)
	}
	s.demoteOrphanedTasks()
}

// GetStorageDir returns the current storage directory.
func (s *Store) GetStorageDir() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.storageDir
}

// ReloadFromDisk re-reads all task files from the storage directory.
// This picks up changes made by other processes (e.g., background agents).
// No-op if no storage directory is configured or if directory hasn't been modified.
func (s *Store) ReloadFromDisk() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.storageDir == "" {
		return
	}

	// Check if any task file has been modified since last reload.
	// We can't rely on directory mtime alone because modifying existing
	// files in-place doesn't update the directory mtime on macOS/most
	// filesystems.
	entries, err := os.ReadDir(s.storageDir)
	if err != nil {
		return
	}

	changed := false
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(s.lastDirMod) {
			changed = true
			break
		}
	}

	if !changed && !s.lastDirMod.IsZero() {
		return
	}

	s.lastDirMod = time.Now()
	_ = s.loadFromDisk()
}

// UpdateOption is a functional option for updating a task
type UpdateOption func(*Task)

// WithStatus sets the task status and records the status change timestamp.
func WithStatus(status string) UpdateOption {
	return func(t *Task) {
		if t.Status != status {
			t.StatusChangedAt = time.Now()
		}
		t.Status = status
	}
}

// WithSubject sets the task subject
func WithSubject(subject string) UpdateOption {
	return func(t *Task) {
		t.Subject = subject
	}
}

// WithDescription sets the task description
func WithDescription(description string) UpdateOption {
	return func(t *Task) {
		t.Description = description
	}
}

// WithActiveForm sets the task activeForm
func WithActiveForm(activeForm string) UpdateOption {
	return func(t *Task) {
		t.ActiveForm = activeForm
	}
}

// WithOwner sets the task owner
func WithOwner(owner string) UpdateOption {
	return func(t *Task) {
		t.Owner = owner
	}
}

// WithMetadata merges metadata (nil values delete keys)
func WithMetadata(metadata map[string]any) UpdateOption {
	return func(t *Task) {
		if t.Metadata == nil {
			t.Metadata = make(map[string]any)
		}
		for k, v := range metadata {
			if v == nil {
				delete(t.Metadata, k)
			} else {
				t.Metadata[k] = v
			}
		}
	}
}

// WithAddBlocks adds task IDs that this task blocks
func WithAddBlocks(ids []string) UpdateOption {
	return func(t *Task) {
		t.Blocks = appendUnique(t.Blocks, ids)
	}
}

// WithAddBlockedBy adds task IDs that block this task
func WithAddBlockedBy(ids []string) UpdateOption {
	return func(t *Task) {
		t.BlockedBy = appendUnique(t.BlockedBy, ids)
	}
}

// normalizeTaskSlices ensures Blocks and BlockedBy are non-nil slices.
func normalizeTaskSlices(t *Task) {
	if t.Blocks == nil {
		t.Blocks = []string{}
	}
	if t.BlockedBy == nil {
		t.BlockedBy = []string{}
	}
}

// FindByMetadata returns a copy of the first non-deleted task whose metadata[key] equals want.
// Returns nil if no match is found.
func (s *Store) FindByMetadata(key, want string) *Task {
	if want == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, t := range s.tasks {
		if t.Status == StatusDeleted {
			continue
		}
		if t.Metadata == nil {
			continue
		}
		if v, ok := t.Metadata[key]; ok {
			if str, ok := v.(string); ok && str == want {
				cp := *t
				return &cp
			}
		}
	}
	return nil
}

// compareNumericIDs compares two task IDs numerically (e.g. "2" < "10").
// Falls back to lexicographic comparison if parsing fails.
func compareNumericIDs(a, b string) bool {
	na, errA := strconv.Atoi(a)
	nb, errB := strconv.Atoi(b)
	if errA == nil && errB == nil {
		return na < nb
	}
	return a < b
}

// appendUnique appends ids to slice, skipping duplicates
func appendUnique(slice, ids []string) []string {
	existing := make(map[string]bool, len(slice))
	for _, id := range slice {
		existing[id] = true
	}
	for _, id := range ids {
		if !existing[id] {
			slice = append(slice, id)
			existing[id] = true
		}
	}
	return slice
}
