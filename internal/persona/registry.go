package persona

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/genai-io/san/internal/confdir"
)

var (
	mu         sync.RWMutex
	defaultReg *Registry
)

// Default returns the package-level Registry, or a registry holding only the
// virtual default if Initialize has not been called.
func Default() *Registry {
	mu.RLock()
	defer mu.RUnlock()
	if defaultReg == nil {
		def := DefaultPersona()
		return &Registry{
			byName:   map[string]*Persona{DefaultName: def},
			personas: []*Persona{def},
		}
	}
	return defaultReg
}

// SetDefault replaces the package-level singleton.
func SetDefault(r *Registry) {
	mu.Lock()
	defer mu.Unlock()
	defaultReg = r
}

// Initialize creates a Registry for the given cwd, ensures the user-level
// personas directory exists with its README, and installs the result as the
// default singleton. Safe to call repeatedly (e.g. on cwd change).
func Initialize(cwd string) {
	_ = EnsureUserDir() // best-effort; ignore errors on read-only homes
	SetDefault(NewRegistry(cwd))
}

// Registry holds the personas discovered on disk plus the virtual default. It
// scans both ~/.san/personas/ and <cwd>/.san/personas/.
//
// The Registry is read-only after construction; the active persona is stored
// in settings, not in the registry.
type Registry struct {
	mu       sync.RWMutex
	cwd      string
	personas []*Persona // default + loaded, in display order
	byName   map[string]*Persona
}

// NewRegistry creates a Registry and loads personas from disk. If cwd is
// empty, only user-level personas are loaded.
func NewRegistry(cwd string) *Registry {
	r := &Registry{cwd: cwd}
	r.reload()
	return r
}

// Reload re-scans the user and project persona directories.
func (r *Registry) Reload() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reload()
}

func (r *Registry) reload() {
	// Real persona directories are authoritative.
	items := []*Persona{DefaultPersona()}
	if home, err := os.UserHomeDir(); err == nil {
		items = append(items, loadDir(filepath.Join(confdir.Dir(home), "personas"), ScopeUser)...)
	}
	if r.cwd != "" {
		items = append(items, loadDir(filepath.Join(confdir.Dir(r.cwd), "personas"), ScopeProject)...)
	}
	byName := dedupeByScope(items)

	// Legacy single-file identities fill in any name a persona directory has
	// not already claimed — personas absorb the older identity files.
	var legacy []*Persona
	if home, err := os.UserHomeDir(); err == nil {
		legacy = append(legacy, loadIdentities(filepath.Join(confdir.Dir(home), "identities"), ScopeUser)...)
	}
	if r.cwd != "" {
		legacy = append(legacy, loadIdentities(filepath.Join(confdir.Dir(r.cwd), "identities"), ScopeProject)...)
	}
	for name, p := range dedupeByScope(legacy) {
		if _, taken := byName[name]; !taken {
			byName[name] = p
		}
	}

	final := make([]*Persona, 0, len(byName))
	for _, it := range byName {
		final = append(final, it)
	}
	sortPersonas(final)

	r.personas = final
	r.byName = byName
}

// dedupeByScope keeps the highest-scope entry per name (project > user >
// builtin) — project overrides user when names collide.
func dedupeByScope(items []*Persona) map[string]*Persona {
	byName := make(map[string]*Persona, len(items))
	for _, it := range items {
		if existing, ok := byName[it.Name]; !ok || it.Scope > existing.Scope {
			byName[it.Name] = it
		}
	}
	return byName
}

// List returns all personas in display order (default first).
func (r *Registry) List() []*Persona {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Persona, len(r.personas))
	copy(out, r.personas)
	return out
}

// Get looks up a persona by name. Returns (nil, false) for unknown names.
// "default" returns the virtual built-in.
func (r *Registry) Get(name string) (*Persona, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.byName[name]
	return p, ok
}

// loadDir scans a single personas root, treating each subdirectory as a
// persona named after it. The reserved "default" name is skipped.
func loadDir(root string, scope Scope) []*Persona {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []*Persona
	for _, e := range entries {
		if !e.IsDir() || strings.EqualFold(e.Name(), DefaultName) {
			continue
		}
		p, ok := parseDir(filepath.Join(root, e.Name()))
		if !ok {
			continue
		}
		p.Scope = scope
		out = append(out, p)
	}
	return out
}
