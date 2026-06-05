package identity

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/genai-io/san/v2/internal/confdir"
)

var (
	mu         sync.RWMutex
	defaultReg *Registry
)

// Default returns the package-level Registry, or an empty one if Initialize
// has not been called.
func Default() *Registry {
	mu.RLock()
	defer mu.RUnlock()
	if defaultReg == nil {
		def := DefaultIdentity()
		return &Registry{
			byName:     map[string]*Identity{DefaultName: def},
			identities: []*Identity{def},
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
// identities directory exists with its README, and installs the result as
// the default singleton. Safe to call repeatedly (e.g. on cwd change).
func Initialize(cwd string) {
	_ = EnsureUserDir() // best-effort; ignore errors on read-only homes
	SetDefault(NewRegistry(cwd))
}

// Registry holds the set of available identities loaded from disk plus the
// virtual default. It is created with a cwd; both ~/.san/identities/ and
// <cwd>/.san/identities/ are scanned.
//
// The Registry is read-only after construction. Activation is stored in
// settings (the Identity field), not in the registry.
type Registry struct {
	mu         sync.RWMutex
	cwd        string
	identities []*Identity // default + loaded files, in display order
	byName     map[string]*Identity
}

// NewRegistry creates a Registry and loads identities from disk. If cwd is
// empty, only user-level identities are loaded.
func NewRegistry(cwd string) *Registry {
	r := &Registry{cwd: cwd}
	r.reload()
	return r
}

// Reload re-scans the user and project identity directories.
func (r *Registry) Reload() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reload()
}

func (r *Registry) reload() {
	items := []*Identity{DefaultIdentity()}

	if home, err := os.UserHomeDir(); err == nil {
		items = append(items, loadDir(filepath.Join(confdir.Dir(home), "identities"), ScopeUser)...)
	}
	if r.cwd != "" {
		items = append(items, loadDir(filepath.Join(confdir.Dir(r.cwd), "identities"), ScopeProject)...)
	}

	// Project overrides user when names collide; keep highest-priority entry.
	byName := make(map[string]*Identity, len(items))
	for _, it := range items {
		if existing, ok := byName[it.Name]; !ok || it.Scope > existing.Scope {
			byName[it.Name] = it
		}
	}
	final := make([]*Identity, 0, len(byName))
	for _, it := range byName {
		final = append(final, it)
	}
	sortIdentities(final)

	r.identities = final
	r.byName = byName
}

// List returns all identities in display order (default first).
func (r *Registry) List() []*Identity {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Identity, len(r.identities))
	copy(out, r.identities)
	return out
}

// Get looks up an identity by name. Returns (nil, false) for unknown names.
// "default" returns the virtual built-in.
func (r *Registry) Get(name string) (*Identity, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.byName[name]
	return id, ok
}

// Active returns the identity body to inject. If name is "" or DefaultName,
// returns "" (caller should let the catalog use the built-in default).
// If name does not resolve, also returns "" — caller should fall back.
func (r *Registry) Active(name string) string {
	if name == "" || name == DefaultName {
		return ""
	}
	id, ok := r.Get(name)
	if !ok || id.IsBuiltin() {
		return ""
	}
	return id.Body
}

// loadDir scans a single identity directory.
func loadDir(dir string, scope Scope) []*Identity {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []*Identity
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		// Skip the documentation README written by template.go.
		if strings.EqualFold(e.Name(), "README.md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		id, err := parseFile(path)
		if err != nil {
			continue
		}
		if strings.EqualFold(id.Name, DefaultName) {
			continue
		}
		id.Scope = scope
		out = append(out, id)
	}
	return out
}

func filenameStem(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
