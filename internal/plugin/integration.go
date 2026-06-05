// Package plugin provides integration helpers for loading plugin components
// into the skill, agent, hooks, and MCP registries.
package plugin

import (
	"context"
	"strings"
	"sync/atomic"

	"github.com/genai-io/san/v2/internal/setting"
)

// GetPluginSkillPaths returns all skill directory paths from enabled plugins.
func GetPluginSkillPaths() []PluginPath {
	return collectPluginPaths(func(p *Plugin) []string { return p.Components.Skills })
}

// GetPluginAgentPaths returns all agent file paths from enabled plugins.
func GetPluginAgentPaths() []PluginPath {
	return collectPluginPaths(func(p *Plugin) []string { return p.Components.Agents })
}

// GetPluginCommandPaths returns all command file paths from enabled plugins.
func GetPluginCommandPaths() []PluginPath {
	return collectPluginPaths(func(p *Plugin) []string { return p.Components.Commands })
}

// collectPluginPaths collects paths from enabled plugins using a getter function.
func collectPluginPaths(getPaths func(*Plugin) []string) []PluginPath {
	var paths []PluginPath
	for _, p := range defaultRegistry.GetEnabled() {
		for _, path := range getPaths(p) {
			paths = append(paths, PluginPath{
				Path:      path,
				Namespace: p.Name(),
				Scope:     p.Scope,
			})
		}
	}
	return paths
}

// PluginPath represents a path with plugin metadata.
type PluginPath struct {
	Path      string
	Namespace string // Plugin name, used as default namespace
	Scope     Scope
}

// GetPluginHooks returns all hooks from enabled plugins in setting.Hook format.
// This can be merged with the application's settings.Hooks.
func GetPluginHooks() map[string][]setting.Hook {
	result := make(map[string][]setting.Hook)

	for _, p := range defaultRegistry.GetEnabled() {
		if p.Components.Hooks == nil {
			continue
		}
		for event, matchers := range p.Components.Hooks.Hooks {
			for _, matcher := range matchers {
				hook := setting.Hook{
					Matcher: matcher.Matcher,
					Hooks:   matcher.Hooks,
				}
				result[event] = append(result[event], hook)
			}
		}
	}

	return result
}

// MergePluginHooksIntoSettings merges plugin hooks into application settings.
// Plugin hooks are appended after the existing hooks for each event.
func MergePluginHooksIntoSettings(settings *setting.Data) {
	if settings.Hooks == nil {
		settings.Hooks = make(map[string][]setting.Hook)
	}

	pluginHooks := GetPluginHooks()
	for event, hooks := range pluginHooks {
		settings.Hooks[event] = append(settings.Hooks[event], hooks...)
	}
}

// PluginMCPServer represents an MCP server from a plugin with full metadata.
type PluginMCPServer struct {
	Name   string          // Full name with namespace (e.g., "plugin:server")
	Config MCPServerConfig // Server configuration
	Scope  Scope           // Plugin scope
}

// GetPluginMCPServers returns all MCP servers from enabled plugins.
func GetPluginMCPServers() []PluginMCPServer {
	var servers []PluginMCPServer
	for _, p := range defaultRegistry.GetEnabled() {
		for name, cfg := range p.Components.MCP {
			servers = append(servers, PluginMCPServer{
				Name:   p.Name() + ":" + name,
				Config: cfg,
				Scope:  p.Scope,
			})
		}
	}
	return servers
}

// GetPluginNamespace extracts the namespace from a plugin path or source.
func GetPluginNamespace(source string) string {
	name, _ := ParsePluginRef(source)
	return name
}

// rootKey is the context-key type used to attach an active plugin root.
type rootKey struct{}

// WithRoot returns a context that carries `path` as the active plugin
// root. Subprocess spawn paths that read PluginEnv(ctx) will then emit
// PLUGIN_ROOT=<path> for hook scripts and tool subprocesses to find
// their sibling files. Pass "" to leave ctx unchanged.
func WithRoot(ctx context.Context, path string) context.Context {
	if path == "" {
		return ctx
	}
	return context.WithValue(ctx, rootKey{}, path)
}

// RootFromContext returns the active plugin root attached to ctx, if any.
func RootFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	v, ok := ctx.Value(rootKey{}).(string)
	if !ok || v == "" {
		return "", false
	}
	return v, true
}

// rootProvider is a registered fallback that supplies the active plugin
// root when ctx doesn't carry one. The app layer wires this to the
// foreground agent task's per-turn scope so deep-inside-the-agent hook
// firings still see the right PLUGIN_ROOT without threading ctx by hand.
var rootProvider atomic.Value // stores func() string

// SetRootProvider registers a fallback supplier of the active plugin
// root, used when ctx doesn't carry one. Pass nil to clear.
func SetRootProvider(fn func() string) {
	if fn == nil {
		rootProvider.Store((func() string)(nil))
		return
	}
	rootProvider.Store(fn)
}

// activePluginRoot resolves the active plugin root: ctx first, then the
// registered provider, else "".
func activePluginRoot(ctx context.Context) string {
	if r, ok := RootFromContext(ctx); ok {
		return r
	}
	if fn, ok := rootProvider.Load().(func() string); ok && fn != nil {
		return fn()
	}
	return ""
}

// FindPluginRootForPath returns the plugin root that contains the given path,
// or "" if no enabled plugin matches.
func FindPluginRootForPath(path string) string {
	if path == "" {
		return ""
	}
	for _, p := range defaultRegistry.GetEnabled() {
		if strings.HasPrefix(path, p.Path+"/") || path == p.Path {
			return p.Path
		}
	}
	return ""
}

// PluginEnv returns environment variables for all enabled plugins,
// resolved for a subprocess spawned under ctx. Callers append the
// result to os.Environ() when starting hook scripts or bash subprocs.
//
// Per plugin (always emitted):
//
//	SAN_PLUGIN_ROOT_<UPPER_NAME>=<path>   CLAUDE_PLUGIN_ROOT_<UPPER_NAME>=<path>
//
// Unqualified alias (active plugin root) — sourced in priority order:
//
//  1. The active root attached to ctx via WithRoot
//
//  2. The registered fallback (SetRootProvider)
//
//  3. The sole enabled plugin's path, if exactly one is enabled
//
//     SAN_PLUGIN_ROOT=<path>   CLAUDE_PLUGIN_ROOT=<path>
func PluginEnv(ctx context.Context) []string {
	enabled := defaultRegistry.GetEnabled()
	if len(enabled) == 0 {
		return nil
	}

	var out []string
	for _, p := range enabled {
		out = append(out, setting.EnvPairF("PLUGIN_ROOT_%s", envSafeName(p.Name()), p.Path)...)
	}

	root := activePluginRoot(ctx)
	if root == "" && len(enabled) == 1 {
		root = enabled[0].Path
	}
	if root != "" {
		out = append(out, setting.EnvPair("PLUGIN_ROOT", root)...)
	}
	return out
}

// envSafeName converts a plugin name to an environment-variable-safe
// upper-case identifier: lowercase, hyphens/dots → underscores.
func envSafeName(name string) string {
	s := strings.ToUpper(name)
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, ".", "_")
	return s
}
