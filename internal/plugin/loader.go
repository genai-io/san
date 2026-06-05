package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/genai-io/san/v2/internal/confdir"
)

const (
	// SanPluginDir is the directory containing plugin metadata for San
	SanPluginDir = ".san-plugin"

	// ClaudePluginDir is the directory containing plugin metadata for Claude Code
	ClaudePluginDir = ".claude-plugin"

	// ManifestFile is the plugin manifest filename
	ManifestFile = "plugin.json"
)

// LoadPlugin loads a plugin from a directory.
// It looks for .san-plugin/plugin.json or .claude-plugin/plugin.json.
func LoadPlugin(path string, scope Scope, source string) (*Plugin, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("invalid plugin path: %w", err)
	}

	// Verify directory exists
	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("plugin path not found: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("plugin path is not a directory: %s", absPath)
	}

	plugin := &Plugin{
		Path:   absPath,
		Scope:  scope,
		Source: source,
	}

	// Try to load manifest
	manifest, err := loadManifest(absPath)
	if err != nil {
		// If no manifest, try to infer plugin name from directory
		plugin.Manifest = Manifest{
			Name: filepath.Base(absPath),
		}
	} else {
		plugin.Manifest = *manifest
	}

	// Resolve all components
	plugin.Components = resolveComponents(&plugin.Manifest, absPath)

	return plugin, nil
}

// loadManifest loads the plugin manifest from .san-plugin or .claude-plugin
// (in that order of preference).
func loadManifest(pluginPath string) (*Manifest, error) {
	for _, metaDir := range []string{SanPluginDir, ClaudePluginDir} {
		path := filepath.Join(pluginPath, metaDir, ManifestFile)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var manifest Manifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			return nil, fmt.Errorf("invalid manifest %s: %w", path, err)
		}
		return &manifest, nil
	}

	return nil, fmt.Errorf("no plugin manifest found in %s", pluginPath)
}

// resolveComponents resolves all component paths for a plugin.
func resolveComponents(manifest *Manifest, pluginPath string) Components {
	return Components{
		Commands: ResolveCommands(manifest, pluginPath),
		Skills:   ResolveSkills(manifest, pluginPath),
		Agents:   ResolveAgents(manifest, pluginPath),
		Hooks:    ResolveHooksConfig(manifest.Hooks, pluginPath),
		MCP:      ResolveMCPServers(manifest.MCPServers, pluginPath),
		LSP:      ResolveLSPServers(manifest.LSPServers, pluginPath),
	}
}

// reservedPluginDirs are directories in the plugins folder that are not plugins.
var reservedPluginDirs = map[string]bool{
	"marketplaces": true, // Stores cloned marketplace repos
	"cache":        true, // Stores plugin cache
}

// LoadPluginsFromDir loads all plugins from a directory.
// Each subdirectory is treated as a potential plugin.
func LoadPluginsFromDir(dir string, scope Scope, sourcePrefix string) ([]*Plugin, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var plugins []*Plugin
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		name := entry.Name()

		// Skip hidden directories (except .claude* and .san*)
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(name, ".claude") && !strings.HasPrefix(name, ".san") {
			continue
		}

		// Skip reserved system directories
		if reservedPluginDirs[name] {
			continue
		}

		pluginPath := filepath.Join(dir, name)
		source := name
		if sourcePrefix != "" {
			source = name + "@" + sourcePrefix
		}

		plugin, err := LoadPlugin(pluginPath, scope, source)
		if err != nil {
			// Log error but continue loading other plugins
			continue
		}
		plugins = append(plugins, plugin)
	}

	return plugins, nil
}

// LoadInstalledPlugins loads plugins listed in installed_plugins.json.
// Supports both v1 (array) and v2 (map) formats.
func LoadInstalledPlugins(installedFile string, scope Scope) ([]*Plugin, error) {
	data, err := os.ReadFile(installedFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	// Try v2 format first
	var v2 InstalledPluginsV2
	if err := json.Unmarshal(data, &v2); err == nil && v2.Version == 2 {
		return loadPluginsFromV2(v2, scope)
	}

	// Fall back to v1 format (array)
	var installed []InstalledPlugin
	if err := json.Unmarshal(data, &installed); err != nil {
		return nil, fmt.Errorf("invalid installed plugins file: %w", err)
	}

	var plugins []*Plugin
	for _, inst := range installed {
		plugin, err := LoadPlugin(inst.Path, scope, inst.Source)
		if err != nil {
			continue
		}
		plugins = append(plugins, plugin)
	}

	return plugins, nil
}

// loadPluginsFromV2 loads plugins from v2 format.
func loadPluginsFromV2(v2 InstalledPluginsV2, scope Scope) ([]*Plugin, error) {
	var plugins []*Plugin

	for source, installs := range v2.Plugins {
		info := findInstallForScope(installs, scope)
		if info == nil {
			continue
		}
		plugin, err := LoadPlugin(info.InstallPath, Scope(info.Scope), source)
		if err != nil {
			continue
		}
		plugins = append(plugins, plugin)
	}

	return plugins, nil
}

// findInstallForScope returns the installation matching the scope, or the first one.
func findInstallForScope(installs []PluginInstallInfo, scope Scope) *PluginInstallInfo {
	for i := range installs {
		if Scope(installs[i].Scope) == scope {
			return &installs[i]
		}
	}
	if len(installs) > 0 {
		return &installs[0]
	}
	return nil
}

// ValidatePlugin validates a plugin directory structure.
func ValidatePlugin(path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("path not found: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", absPath)
	}

	// Check for manifest
	manifest, err := loadManifest(absPath)
	if err != nil {
		return fmt.Errorf("no valid manifest: %w", err)
	}

	// Validate manifest fields
	if manifest.Name == "" {
		return fmt.Errorf("manifest missing required 'name' field")
	}

	// Validate version format if present
	if manifest.Version != "" && !isValidSemver(manifest.Version) {
		return fmt.Errorf("invalid version format: %s (expected semver)", manifest.Version)
	}

	return nil
}

// isValidSemver performs basic semver validation.
func isValidSemver(v string) bool {
	v = strings.TrimPrefix(v, "v")
	parts := strings.Split(v, ".")
	if len(parts) < 1 || len(parts) > 4 {
		return false
	}
	// First part must be numeric
	for _, c := range parts[0] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// GetPluginDirs returns the standard plugin directories for each scope.
// Note: cache directory is NOT included here - it's only accessed via installed_plugins.json
func GetPluginDirs(cwd string) map[Scope][]string {
	homeDir, _ := os.UserHomeDir()

	return map[Scope][]string{
		ScopeUser: {
			filepath.Join(confdir.Dir(homeDir), "plugins"),
		},
		ScopeProject: {
			filepath.Join(confdir.Dir(cwd), "plugins"),
		},
		ScopeLocal: {
			filepath.Join(confdir.Dir(cwd), "plugins-local"),
		},
	}
}

// GetInstalledPluginsFile returns the path to installed_plugins.json for a scope.
func GetInstalledPluginsFile(cwd string, scope Scope) string {
	dirs := GetPluginDirs(cwd)
	if paths, ok := dirs[scope]; ok && len(paths) > 0 {
		return filepath.Join(paths[0], "installed_plugins.json")
	}
	return ""
}
