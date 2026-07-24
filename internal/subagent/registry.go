package subagent

import (
	"sort"
	"strings"
	"sync"
)

// Registry manages agent definitions.
type Registry struct {
	mu           sync.RWMutex
	agents       map[string]*AgentConfig
	userStore    *AgentStore     // User-level enabled/disabled states
	projectStore *AgentStore     // Project-level enabled/disabled states
	personaAllow map[string]bool // active persona's visible-agent allow-list (nil = no restriction)
	cwd          string          // Current working directory
}

// NewRegistry creates an empty registry for user, project, and plugin-defined agents.
func NewRegistry() *Registry {
	return &Registry{agents: make(map[string]*AgentConfig)}
}

// Register adds an agent configuration to the registry
func (r *Registry) Register(config *AgentConfig) {
	config.Name = strings.TrimSpace(config.Name)
	if config.Name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agents[strings.ToLower(config.Name)] = config
}

// Get retrieves an agent configuration by name
func (r *Registry) Get(name string) (*AgentConfig, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	config, ok := r.agents[strings.ToLower(name)]
	return config, ok
}

// ResolveEnabledAgent returns an enabled Agent configuration by exact name.
func (r *Registry) ResolveEnabledAgent(name string) (*AgentConfig, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	lowerName := strings.ToLower(strings.TrimSpace(name))
	config, ok := r.agents[lowerName]
	if !ok || r.isDisabledInternal(lowerName) {
		return nil, false
	}
	return config, true
}

// ListConfigs returns all registered agent configurations that are visible
// under the active persona allow-list.
func (r *Registry) ListConfigs() []*AgentConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	configs := make([]*AgentConfig, 0, len(r.agents))
	for name, config := range r.agents {
		if r.personaAllow != nil && !r.personaAllow[name] {
			continue
		}
		configs = append(configs, config)
	}
	return configs
}

// InitStores initializes the user and project stores for enabled/disabled state
func (r *Registry) InitStores(cwd string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.cwd = cwd
	r.userStore = NewUserAgentStore()
	r.projectStore = NewProjectAgentStore(cwd)
	return nil
}

// IsEnabled returns whether an agent is enabled
// An agent is enabled unless explicitly disabled in either store
// Project-level settings take priority over user-level
func (r *Registry) IsEnabled(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	lowerName := strings.ToLower(name)

	// The active persona's allow-list restricts the visible set: an agent not
	// on it is treated as disabled while that persona is selected.
	if r.personaAllow != nil && !r.personaAllow[lowerName] {
		return false
	}

	// Check project store first (higher priority)
	if r.projectStore != nil && r.projectStore.IsDisabled(lowerName) {
		return false
	}

	// Check user store
	if r.userStore != nil && r.userStore.IsDisabled(lowerName) {
		return false
	}

	return true
}

// SetEnabled sets the enabled state for an agent at the specified level.
// Used by internal/app's agentRegistryAdapter.
func (r *Registry) SetEnabled(name string, enabled bool, userLevel bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	lowerName := strings.ToLower(name)

	if userLevel {
		if r.userStore != nil {
			return r.userStore.SetDisabled(lowerName, !enabled)
		}
	} else {
		if r.projectStore != nil {
			return r.projectStore.SetDisabled(lowerName, !enabled)
		}
	}
	return nil
}

// GetDisabledAt returns the disabled agents from the specified level.
// Used by internal/app's agentRegistryAdapter.
func (r *Registry) GetDisabledAt(userLevel bool) map[string]bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if userLevel {
		if r.userStore != nil {
			return r.userStore.GetDisabled()
		}
	} else {
		if r.projectStore != nil {
			return r.projectStore.GetDisabled()
		}
	}
	return make(map[string]bool)
}

// LoadPersona restricts the visible agent set to an allow-list while a persona
// is active: only the named agents are spawnable and shown in the agents
// directory. An empty/blank list clears the restriction (all agents visible).
// In-memory only — never written to the user/project enable-disable stores, so
// it composes with them and disappears when the persona is cleared.
func (r *Registry) LoadPersona(allow []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	m := make(map[string]bool, len(allow))
	for _, n := range allow {
		if n = strings.ToLower(strings.TrimSpace(n)); n != "" {
			m[n] = true
		}
	}
	if len(m) == 0 {
		r.personaAllow = nil
		return
	}
	r.personaAllow = m
}

// ClearPersona removes any persona allow-list, making all agents visible again
// (subject to the user/project enable-disable stores).
func (r *Registry) ClearPersona() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.personaAllow = nil
}

// isDisabledInternal checks if an agent is disabled (must be called with lock held)
func (r *Registry) isDisabledInternal(name string) bool {
	if r.personaAllow != nil && !r.personaAllow[name] {
		return true
	}
	if r.projectStore != nil && r.projectStore.IsDisabled(name) {
		return true
	}
	if r.userStore != nil && r.userStore.IsDisabled(name) {
		return true
	}
	return false
}

// GetAgentsSection returns the body of the agents directory for the system
// prompt. Only enabled agents, sorted by name (deterministic output).
//
// Returns plain body text without the outer XML tag; the system catalog
// wraps it in <agents>…</agents>.
func (r *Registry) GetAgentsSection() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	type entry struct {
		name, desc, whenToUse, tools string
	}

	var entries []entry
	for name, config := range r.agents {
		if r.isDisabledInternal(name) {
			continue
		}
		toolsDesc := "*"
		if config.AllowTools != nil {
			toolsDesc = strings.Join(config.AllowTools.DisplayNames(), ", ")
		}
		entries = append(entries, entry{
			name:      config.Name,
			desc:      config.Description,
			whenToUse: config.WhenToUse,
			tools:     toolsDesc,
		})
	}

	if len(entries) == 0 {
		return ""
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].name < entries[j].name
	})

	var sb strings.Builder
	sb.WriteString("Available agents for the Agent tool:\n\n")
	for i, e := range entries {
		sb.WriteString("- " + e.name + ": " + e.desc)
		if e.whenToUse != "" {
			sb.WriteString("\n  Use when: " + e.whenToUse)
		}
		sb.WriteString("\n  Tools: " + e.tools)
		if i < len(entries)-1 {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// PromptSection returns the rendered prompt section for available agents.
func (r *Registry) PromptSection() string {
	return r.GetAgentsSection()
}
