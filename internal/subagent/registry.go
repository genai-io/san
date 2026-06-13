package subagent

import (
	"sort"
	"strings"
	"sync"
)

// Registry manages agent type definitions
type Registry struct {
	mu           sync.RWMutex
	agents       map[string]*AgentConfig
	userStore    *AgentStore     // User-level enabled/disabled states
	projectStore *AgentStore     // Project-level enabled/disabled states
	personaAllow map[string]bool // active persona's visible-agent allow-list (nil = no restriction)
	cwd          string          // Current working directory
}

// NewRegistry creates a new agent registry
func NewRegistry() *Registry {
	r := &Registry{
		agents: make(map[string]*AgentConfig),
	}
	// Register built-in agents
	r.registerBuiltins()
	return r
}

// defaultRegistry is the package-level agent registry.
// External callers should use Default() to get the Service singleton.
var defaultRegistry = NewRegistry()

// Register adds an agent configuration to the registry
func (r *Registry) Register(config *AgentConfig) {
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

// ListConfigs returns all registered agent configurations
func (r *Registry) ListConfigs() []*AgentConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	configs := make([]*AgentConfig, 0, len(r.agents))
	for _, config := range r.agents {
		configs = append(configs, config)
	}
	return configs
}

// registerBuiltins registers the built-in agent types
func (r *Registry) registerBuiltins() {
	// General-purpose agent - all tools (including nested Agent)
	r.agents["general-purpose"] = &AgentConfig{
		Name:        "general-purpose",
		Description: "General-purpose agent for researching complex questions, searching for code, and executing multi-step tasks.",
		Color:       "green",
		WhenToUse: `Use this when the task needs multiple searches, cross-file reasoning, implementation work, or another multi-step workflow.
For non-mutating investigation, run this agent with mode=explore. For implementation work, run it with mode=acceptEdits or the default mode.`,
		Model:          "inherit",
		PermissionMode: PermissionDefault,
		AllowTools:     nil,
		MaxSteps:       100,
		Source:         "built-in",
	}

	// code-simplifier agent - simplifies and refines code
	r.agents["code-simplifier"] = &AgentConfig{
		Name:        "code-simplifier",
		Description: "Simplifies and refines code for clarity, consistency, and maintainability while preserving all functionality.",
		Color:       "blue",
		WhenToUse: `Use this after implementing a feature or making changes to clean up the code.
Focuses on recently modified code unless instructed otherwise.
Good for reducing complexity, removing duplication, improving naming, and tightening logic.
Use it to enforce naming conventions and replace hacks with clear, maintainable, extensible implementations.`,
		Model:          "inherit",
		PermissionMode: PermissionAcceptEdits,
		AllowTools:     nil,
		DenyTools: ToolNames("Agent", "SendMessage",
			"EnterWorktree", "ExitWorktree",
			"CronCreate", "CronDelete", "CronList"),
		MaxSteps: 100,
		Source:   "built-in",
	}

	// code-reviewer agent - reviews code for quality issues without mutating the workspace
	r.agents["code-reviewer"] = &AgentConfig{
		Name:        "code-reviewer",
		Description: "Reviews code changes for bugs, security issues, performance problems, and style violations.",
		Color:       "yellow",
		WhenToUse: `Use this when you want an independent review of code changes before committing or merging.
Good for catching issues you might have missed — security vulnerabilities, edge cases, naming problems, or logic errors.
Returns a structured review with findings and recommendations.`,
		Model:          "inherit",
		PermissionMode: PermissionExplore,
		AllowTools: ToolList{
			{Name: "Read"},
			{Name: "Glob"},
			{Name: "Grep"},
			{Name: "Bash", Pattern: "git diff*"},
			{Name: "Bash", Pattern: "git log*"},
			{Name: "Bash", Pattern: "git show*"},
			{Name: "Bash", Pattern: "git status*"},
			{Name: "WebFetch"},
			{Name: "WebSearch"},
		},
		MaxSteps: 100,
		Source:   "built-in",
	}
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
	sb.WriteString("Available agent types for the Agent tool:\n\n")
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
