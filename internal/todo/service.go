package todo

import "sync"

// Service is the public contract for the tracker module.
type Service interface {
	// CRUD
	Create(subject, description, activeForm string, metadata map[string]any) *Task
	Get(id string) (*Task, bool)
	Update(id string, opts ...UpdateOption) error
	Delete(id string) error
	List() []*Task

	// query
	IsBlocked(id string) bool
	OpenBlockers(id string) []string
	HasInProgress() bool
	AllDone() bool
	FindByMetadata(key, want string) *Task

	// persistence
	SetStorageDir(dir string) error
	GetStorageDir() string
	ReloadFromDisk()
	Export() []Task
	Import(tasks []Task)

	// lifecycle
	Reset()
}

// Options holds all dependencies for initialization.
type Options struct{}

// ── singleton ──────────────────────────────────────────────

var (
	mu       sync.RWMutex
	instance Service
)

// Initialize creates a new Store and sets it as the singleton.
func Initialize(opts Options) {
	mu.Lock()
	instance = NewStore()
	mu.Unlock()
}

// Default returns the singleton Service instance.
// Panics if not initialized.
func Default() Service {
	mu.RLock()
	s := instance
	mu.RUnlock()
	if s == nil {
		panic("tracker: not initialized")
	}
	return s
}

// SetDefault replaces the singleton instance. Intended for tests.
func SetDefault(s Service) {
	mu.Lock()
	instance = s
	mu.Unlock()
}

// ResetService clears the singleton instance. Intended for tests.
func ResetService() {
	mu.Lock()
	instance = nil
	mu.Unlock()
}
