package plugin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"

	"github.com/genai-io/san/internal/atomicfile"
	"github.com/genai-io/san/internal/confdir"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/setting"
)

// Registry manages all loaded plugins.
type Registry struct {
	mu      sync.RWMutex
	plugins map[string]*Plugin // key: plugin full name (e.g., "git@marketplace")
	cwd     string             // Current working directory
	loaded  bool               // Whether plugins have been loaded
}

// NewRegistry creates a new plugin registry.
func NewRegistry() *Registry {
	return &Registry{
		plugins: make(map[string]*Plugin),
	}
}

// Load loads all plugins from user, project, and local scopes.
func (r *Registry) Load(ctx context.Context, cwd string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.cwd = cwd
	r.plugins = make(map[string]*Plugin)

	// An untrusted folder's plugins wait for trust: a plugin carries hooks and
	// MCP servers.
	scopes := []Scope{ScopeUser}
	if setting.IsWorkspaceTrusted(cwd) {
		scopes = append(scopes, ScopeProject, ScopeLocal)
	}

	// Settings apply the same trust rules to enabledPlugins.
	enabledPlugins := map[string]bool{}
	if s, err := setting.LoadForCwd(cwd); err == nil {
		enabledPlugins = s.EnabledPlugins
	}

	// Load plugins from each scope in deterministic precedence order.
	// Later scopes override earlier ones when the same plugin key exists.
	dirsByScope := GetPluginDirs(cwd)
	for _, scope := range scopes {
		dirs := dirsByScope[scope]
		for _, dir := range dirs {
			plugins, err := LoadPluginsFromDir(dir, scope, "")
			if err != nil {
				continue
			}
			for _, p := range plugins {
				key := p.FullName()
				// Check if enabled
				if enabled, ok := enabledPlugins[key]; ok {
					p.Enabled = enabled
				} else {
					// Default: user plugins enabled, others disabled
					p.Enabled = scope == ScopeUser
				}
				r.plugins[key] = p
			}
		}
	}

	// Load from installed_plugins.json files
	for _, scope := range scopes {
		installedFile := GetInstalledPluginsFile(cwd, scope)
		plugins, err := LoadInstalledPlugins(installedFile, scope)
		if err != nil {
			continue
		}
		for _, p := range plugins {
			// A project/local install copies the plugin into a scope dir, so the
			// scope scan above already loaded it under a bare name. Drop that
			// duplicate — this entry carries the real "name@marketplace" source.
			for k, existing := range r.plugins {
				if existing.Path == p.Path {
					delete(r.plugins, k)
				}
			}
			key := p.FullName()
			if enabled, ok := enabledPlugins[key]; ok {
				p.Enabled = enabled
			} else {
				p.Enabled = true // Installed plugins default to enabled
			}
			r.plugins[key] = p
		}
	}

	r.loaded = true
	return nil
}

// snapshotLocked copies a plugin for a caller to read outside the lock.
//
// Enable/Disable write p.Enabled under r.mu, and they run on a tea.Cmd
// goroutine during /plugin install — after a git clone that can take the full
// 120s timeout. Handing out the live pointer let the UI read that field while
// it was being written. The Components slices and maps are shared: they are
// built once at load time and never mutated after.
func snapshotLocked(p *Plugin) *Plugin {
	if p == nil {
		return nil
	}
	cp := *p
	return &cp
}

// SetCwd re-points the registry at a working directory. r.cwd is written
// under r.mu by Load and read under it by saveEnabledState, so a caller
// outside the package cannot assign it directly.
func (r *Registry) SetCwd(cwd string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cwd = cwd
}

// Get returns a plugin by name.
func (r *Registry) Get(name string) (*Plugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Try exact match first
	if p, ok := r.plugins[name]; ok {
		return snapshotLocked(p), true
	}

	// Try partial match (name without marketplace)
	for key, p := range r.plugins {
		if p.Name() == name {
			return snapshotLocked(p), true
		}
		// Match the prefix before @
		if idx := len(name); idx < len(key) && key[:idx] == name && key[idx] == '@' {
			return snapshotLocked(p), true
		}
	}

	return nil, false
}

// List returns all plugins sorted by name.
func (r *Registry) List() []*Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()

	plugins := make([]*Plugin, 0, len(r.plugins))
	for _, p := range r.plugins {
		plugins = append(plugins, snapshotLocked(p))
	}

	sort.Slice(plugins, func(i, j int) bool {
		return plugins[i].FullName() < plugins[j].FullName()
	})

	return plugins
}

// ListEnabled returns all enabled plugins.
func (r *Registry) ListEnabled() []*Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var enabled []*Plugin
	for _, p := range r.plugins {
		if p.Enabled {
			enabled = append(enabled, snapshotLocked(p))
		}
	}

	sort.Slice(enabled, func(i, j int) bool {
		return enabled[i].FullName() < enabled[j].FullName()
	})

	return enabled
}

// Enable enables a plugin.
func (r *Registry) Enable(name string, scope Scope) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	p, ok := r.plugins[name]
	if !ok {
		return fmt.Errorf("plugin not found: %s", name)
	}

	p.Enabled = true
	return r.saveEnabledState(name, true, scope)
}

// Disable disables a plugin.
func (r *Registry) Disable(name string, scope Scope) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	p, ok := r.plugins[name]
	if !ok {
		return fmt.Errorf("plugin not found: %s", name)
	}

	p.Enabled = false
	return r.saveEnabledState(name, false, scope)
}

// saveEnabledState persists plugin enabled state to settings.
func (r *Registry) saveEnabledState(name string, enabled bool, scope Scope) error {
	homeDir, _ := os.UserHomeDir()

	var settingsPath string
	switch scope {
	case ScopeUser:
		settingsPath = filepath.Join(confdir.Dir(homeDir), "settings.json")
	case ScopeProject:
		settingsPath = filepath.Join(confdir.Dir(r.cwd), "settings.json")
	case ScopeLocal:
		settingsPath = filepath.Join(confdir.LocalDir(r.cwd), "settings.local.json")
	default:
		settingsPath = filepath.Join(confdir.Dir(homeDir), "settings.json")
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return err
	}

	// Load existing settings
	var settings map[string]any
	if err := atomicfile.ReadJSON(settingsPath, &settings); err != nil {
		return err
	}
	if settings == nil {
		settings = make(map[string]any)
	}

	// Update enabled plugins
	enabledPlugins, _ := settings["enabledPlugins"].(map[string]any)
	if enabledPlugins == nil {
		enabledPlugins = make(map[string]any)
	}
	enabledPlugins[name] = enabled
	settings["enabledPlugins"] = enabledPlugins

	// Write back
	if err := atomicfile.WriteJSON(settingsPath, settings, 0o644); err != nil {
		return err
	}
	hook.FireConfigChange(scopeConfigSource(scope), settingsPath)
	return nil
}

func scopeConfigSource(scope Scope) string {
	switch scope {
	case ScopeProject:
		return "project_settings"
	case ScopeLocal:
		return "local_settings"
	default:
		return "user_settings"
	}
}

// Register adds a plugin to the registry.
func (r *Registry) Register(plugin *Plugin) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.plugins[plugin.FullName()] = plugin
}

// Unregister removes a plugin from the registry.
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.plugins, name)
}

// LoadFromPath loads a plugin from a specific path (for --plugin-dir).
func (r *Registry) LoadFromPath(ctx context.Context, path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	plugin, err := LoadPlugin(path, ScopeLocal, "")
	if err != nil {
		return err
	}

	plugin.Enabled = true // Always enable plugins loaded via --plugin-dir
	r.plugins[plugin.FullName()] = plugin

	return nil
}

// Count returns the number of loaded plugins.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.plugins)
}

// EnabledCount returns the number of enabled plugins.
func (r *Registry) EnabledCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	count := 0
	for _, p := range r.plugins {
		if p.Enabled {
			count++
		}
	}
	return count
}

// defaultRegistry is the package-level plugin registry.
var defaultRegistry = NewRegistry()

// ProjectPluginFindings lists the plugins cwd's own scopes would load, for the
// workspace trust prompt: a plugin can carry hooks and MCP servers.
func ProjectPluginFindings(cwd string) []string {
	dirsByScope := GetPluginDirs(cwd)
	var out []string
	for _, scope := range []Scope{ScopeProject, ScopeLocal} {
		var plugins []*Plugin
		for _, dir := range dirsByScope[scope] {
			found, _ := LoadPluginsFromDir(dir, scope, "")
			plugins = append(plugins, found...)
		}
		installed, _ := LoadInstalledPlugins(GetInstalledPluginsFile(cwd, scope), scope)
		for _, p := range append(plugins, installed...) {
			out = append(out, "load plugin "+p.FullName()+"  ("+string(scope)+" scope)")
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}
