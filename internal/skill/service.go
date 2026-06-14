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

	defaultRegistry = registry
}

// Default returns the package-level *Registry. Returns an empty
// (no-skills) registry pre-Initialize so callers that touch it before
// Initialize don't crash.
func Default() *Registry {
	return defaultRegistry
}

// DefaultIfInit returns the package-level *Registry, or nil if
// Initialize has not yet replaced the empty pre-init instance. Kept
// for callers that want to distinguish "ready" from "not ready"
// states.
func DefaultIfInit() *Registry {
	if defaultRegistry == nil || len(defaultRegistry.skills) == 0 {
		return nil
	}
	return defaultRegistry
}

// SetDefaultRegistry replaces the package-level registry. Intended
// for tests. A nil argument restores a fresh empty *Registry.
func SetDefaultRegistry(r *Registry) {
	if r == nil {
		defaultRegistry = newEmptyRegistry()
		return
	}
	defaultRegistry = r
}

// ResetDefaultRegistry restores a fresh empty *Registry. Intended for
// tests.
func ResetDefaultRegistry() {
	defaultRegistry = newEmptyRegistry()
}

// defaultRegistry is the package-level skill registry.
var defaultRegistry = newEmptyRegistry()

func newEmptyRegistry() *Registry {
	return &Registry{skills: make(map[string]*Skill)}
}
