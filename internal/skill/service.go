// Package skill owns the registry of user/project/plugin-scoped skill
// definitions: their markdown content, per-skill enabled state, and the
// rendering of the active-skills section consumed by core.System.
//
// The package exposes *Registry directly. Skill's consumers (TUI
// selector, slash-command lookup, system-prompt rendering, recorder
// observer) each use a different subset of the registry surface; no
// shared narrow surface ⇒ no producer-side role interface. Consumers
// hold *Registry as an opaque handle and call its methods.
package skill

import "sync"

// PluginSkillPath describes a skill directory provided by a plugin.
type PluginSkillPath struct {
	Path      string
	Namespace string
	IsProject bool // true for project-scope, false for user-scope
}

// Options holds all dependencies for initialization.
type Options struct {
	CWD              string
	PluginSkillPaths func() []PluginSkillPath // injected plugin callback
}

// Initialize loads skills from all sources, applies persisted states,
// and installs the result as the package-level *Registry.
func Initialize(opts Options) {
	cwd := opts.CWD
	loader := newLoader(cwd)
	loader.pluginSkillPaths = opts.PluginSkillPaths

	skills, _ := loader.loadAll()
	userStore, _ := NewUserStore()
	projectStore, _ := NewProjectStore(cwd)

	registry := &Registry{
		skills:       skills,
		userStore:    userStore,
		projectStore: projectStore,
		cwd:          cwd,
	}

	for _, skill := range skills {
		fullName := skill.FullName()
		if state, ok := userStore.GetState(fullName); ok {
			skill.State = state
		}
		if state, ok := projectStore.GetState(fullName); ok {
			skill.State = state
		}
	}

	setDefaultRegistry(registry)
}

// Default returns the package-level *Registry. Returns an empty
// (no-skills) registry pre-Initialize so callers that touch it before
// Initialize don't crash.
func Default() *Registry {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return defaultRegistry
}

// DefaultIfInit returns the package-level *Registry, or nil if
// Initialize has not yet replaced the empty pre-init instance. Kept
// for callers that want to distinguish "ready" from "not ready"
// states.
func DefaultIfInit() *Registry {
	r := Default()
	if r == nil || r.Count() == 0 {
		return nil
	}
	return r
}

// SetDefaultRegistry replaces the package-level registry. Intended
// for tests. A nil argument restores a fresh empty *Registry.
func SetDefaultRegistry(r *Registry) {
	if r == nil {
		r = newEmptyRegistry()
	}
	setDefaultRegistry(r)
}

// ResetDefaultRegistry restores a fresh empty *Registry. Intended for
// tests.
func ResetDefaultRegistry() {
	setDefaultRegistry(newEmptyRegistry())
}

// registryMu guards the defaultRegistry pointer swap; the Registry it points
// at locks its own contents. Initialize (UI goroutine) and Default() (agent
// goroutine) run concurrently, mirroring internal/persona's singleton locking.
var (
	registryMu      sync.RWMutex
	defaultRegistry = newEmptyRegistry()
)

func setDefaultRegistry(r *Registry) {
	registryMu.Lock()
	defer registryMu.Unlock()
	defaultRegistry = r
}

func newEmptyRegistry() *Registry {
	return &Registry{skills: make(map[string]*Skill)}
}
