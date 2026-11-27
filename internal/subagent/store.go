package subagent

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"sync"

	"github.com/genai-io/san/internal/infra/atomicfile"
	"github.com/genai-io/san/internal/infra/confdir"
)

// disabledStoreData is the JSON structure for agents.json
type disabledStoreData struct {
	Disabled []string `json:"disabled"`
}

// disabledStore handles persistence of agent enabled/disabled states
type disabledStore struct {
	mu       sync.RWMutex
	path     string
	disabled map[string]bool
}

// newDisabledStore creates a new store at the given path
func newDisabledStore(path string) *disabledStore {
	store := &disabledStore{
		path:     path,
		disabled: make(map[string]bool),
	}
	store.load()
	return store
}

// newUserDisabledStore creates a store for user-level (~/.san/agents.json)
func newUserDisabledStore() *disabledStore {
	home, err := os.UserHomeDir()
	if err != nil {
		return &disabledStore{disabled: make(map[string]bool)}
	}
	return newDisabledStore(filepath.Join(confdir.Dir(home), "agents.json"))
}

// newProjectDisabledStore creates a store for project-level (.san/agents.json)
func newProjectDisabledStore(cwd string) *disabledStore {
	return newDisabledStore(filepath.Join(confdir.Dir(cwd), "agents.json"))
}

// load reads disabled agents from disk
func (s *disabledStore) load() {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}

	var storeData disabledStoreData
	if err := json.Unmarshal(data, &storeData); err != nil {
		return
	}

	s.disabled = make(map[string]bool)
	for _, name := range storeData.Disabled {
		s.disabled[name] = true
	}
}

// persistDisabled writes the disabled agent list to disk. Lock-free — operates
// only on the provided snapshot.
func persistDisabled(path string, disabled []string) error {
	return atomicfile.WriteJSON(path, disabledStoreData{Disabled: disabled}, 0o644)
}

// IsDisabled returns whether an agent is disabled
func (s *disabledStore) IsDisabled(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.disabled[name]
}

// SetDisabled sets the disabled state for an agent and persists to disk.
func (s *disabledStore) SetDisabled(name string, disabled bool) error {
	s.mu.Lock()
	if disabled {
		s.disabled[name] = true
	} else {
		delete(s.disabled, name)
	}
	// Snapshot while still holding the write lock so no concurrent
	// modification can slip in before we read the state to persist.
	snapshot := make([]string, 0, len(s.disabled))
	for n := range s.disabled {
		snapshot = append(snapshot, n)
	}
	path := s.path
	s.mu.Unlock()

	return persistDisabled(path, snapshot)
}

// Disabled returns a copy of the disabled agents map
func (s *disabledStore) Disabled() map[string]bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]bool, len(s.disabled))
	maps.Copy(result, s.disabled)
	return result
}
