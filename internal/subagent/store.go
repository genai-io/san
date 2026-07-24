package subagent

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"sync"

	"github.com/genai-io/san/internal/atomicfile"
	"github.com/genai-io/san/internal/confdir"
)

// AgentStoreData is the JSON structure for agents.json
type AgentStoreData struct {
	Disabled []string `json:"disabled"`
}

// AgentStore handles persistence of agent enabled/disabled states
type AgentStore struct {
	mu       sync.RWMutex
	path     string
	disabled map[string]bool
}

// NewAgentStore creates a new store at the given path
func NewAgentStore(path string) *AgentStore {
	store := &AgentStore{
		path:     path,
		disabled: make(map[string]bool),
	}
	store.load()
	return store
}

// NewUserAgentStore creates a store for user-level (~/.san/agents.json)
func NewUserAgentStore() *AgentStore {
	home, err := os.UserHomeDir()
	if err != nil {
		return &AgentStore{disabled: make(map[string]bool)}
	}
	return NewAgentStore(filepath.Join(confdir.Dir(home), "agents.json"))
}

// NewProjectAgentStore creates a store for project-level (.san/agents.json)
func NewProjectAgentStore(cwd string) *AgentStore {
	return NewAgentStore(filepath.Join(confdir.Dir(cwd), "agents.json"))
}

// load reads disabled agents from disk
func (s *AgentStore) load() {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}

	var storeData AgentStoreData
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
	return atomicfile.WriteJSON(path, AgentStoreData{Disabled: disabled}, 0o644)
}

// IsDisabled returns whether an agent is disabled
func (s *AgentStore) IsDisabled(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.disabled[name]
}

// SetDisabled sets the disabled state for an agent and persists to disk.
func (s *AgentStore) SetDisabled(name string, disabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	wasDisabled := s.disabled[name]
	if disabled {
		s.disabled[name] = true
	} else {
		delete(s.disabled, name)
	}

	snapshot := make([]string, 0, len(s.disabled))
	for n := range s.disabled {
		snapshot = append(snapshot, n)
	}
	if err := persistDisabled(s.path, snapshot); err != nil {
		if wasDisabled {
			s.disabled[name] = true
		} else {
			delete(s.disabled, name)
		}
		return err
	}
	return nil
}

// GetDisabled returns a copy of the disabled agents map
func (s *AgentStore) GetDisabled() map[string]bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]bool, len(s.disabled))
	maps.Copy(result, s.disabled)
	return result
}
