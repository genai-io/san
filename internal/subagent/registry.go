package subagent

import (
	"strings"
	"sync"
)

// Registry manages custom agent definitions in memory.
type Registry struct {
	mu     sync.RWMutex
	agents map[string]*AgentConfig
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{agents: make(map[string]*AgentConfig)}
}

// Register adds an agent configuration to the registry.
func (r *Registry) Register(config *AgentConfig) {
	config.Name = strings.TrimSpace(config.Name)
	if config.Name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agents[strings.ToLower(config.Name)] = config
}

// Get retrieves an agent configuration by name.
func (r *Registry) Get(name string) (*AgentConfig, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	config, ok := r.agents[strings.ToLower(strings.TrimSpace(name))]
	return config, ok
}

// Resolve returns a custom agent configuration by exact name.
func (r *Registry) Resolve(name string) (*AgentConfig, bool) {
	return r.Get(name)
}

// ListConfigs returns all registered agent configurations.
func (r *Registry) ListConfigs() []*AgentConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	configs := make([]*AgentConfig, 0, len(r.agents))
	for _, config := range r.agents {
		configs = append(configs, config)
	}
	return configs
}
