package todo

import "sync"

var (
	mu       sync.RWMutex
	instance *Store
)

// Initialize creates a new Store and sets it as the singleton.
func Initialize() {
	mu.Lock()
	instance = NewStore()
	mu.Unlock()
}

// Default returns the singleton Store.
// Panics if not initialized.
func Default() *Store {
	mu.RLock()
	s := instance
	mu.RUnlock()
	if s == nil {
		panic("tracker: not initialized")
	}
	return s
}

// SetDefault replaces the singleton instance. Intended for tests.
func SetDefault(s *Store) {
	mu.Lock()
	instance = s
	mu.Unlock()
}
