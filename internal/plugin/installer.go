package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/genai-io/san/internal/confdir"
)

// Installer handles plugin installation and management.
type Installer struct {
	registry           *Registry
	cwd                string
	marketplaces       map[string]MarketplaceSource
	marketplaceManager *MarketplaceManager
}

// NewInstaller creates a new plugin installer.
func NewInstaller(registry *Registry, cwd string) *Installer {
	if registry != nil {
		registry.cwd = cwd
	}
	return &Installer{
		registry:           registry,
		cwd:                cwd,
		marketplaces:       make(map[string]MarketplaceSource),
		marketplaceManager: NewMarketplaceManager(cwd),
	}
}

// LoadMarketplaces loads known marketplace definitions.
func (i *Installer) LoadMarketplaces() error {
	// Load via new marketplace manager
	if err := i.marketplaceManager.Load(); err != nil {
		return err
	}

	// Sync marketplace manager data to i.marketplaces
	for _, id := range i.marketplaceManager.List() {
		entry, ok := i.marketplaceManager.Get(id)
		if !ok {
			continue
		}
		source := MarketplaceSource{
			Name: id,
			Type: entry.Source.Source,
		}
		switch entry.Source.Source {
		case "github":
			source.Repository = entry.Source.Repo
		case "directory":
			source.Path = entry.Source.Path
		}
		i.marketplaces[id] = source
	}

	homeDir, _ := os.UserHomeDir()

	// Also load legacy format for backward compatibility
	paths := []string{
		filepath.Join(confdir.Dir(homeDir), "plugins", "known_marketplaces.json"),
		filepath.Join(confdir.Dir(i.cwd), "plugins", "known_marketplaces.json"),
	}

	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var km KnownMarketplaces
		if err := json.Unmarshal(data, &km); err != nil {
			continue
		}
		for _, m := range km.Marketplaces {
			i.marketplaces[m.Name] = m
		}
	}

	return nil
}

// resolveMarketplaceByName searches for a marketplace whose marketplace.json
// "name" field matches the given name. Returns the marketplace ID if found.
func (i *Installer) resolveMarketplaceByName(name string) string {
	for _, id := range i.marketplaceManager.List() {
		meta, err := i.marketplaceManager.GetMarketplaceMetadata(id)
		if err != nil {
			continue
		}
		if meta.Name == name {
			return id
		}
	}
	return ""
}

// ParsePluginRef parses a plugin reference like "git@my-marketplace" or "git".
func ParsePluginRef(ref string) (name, marketplace string) {
	parts := strings.SplitN(ref, "@", 2)
	name = parts[0]
	if len(parts) > 1 {
		marketplace = parts[1]
	}
	return name, marketplace
}

// FormatPluginRef builds a plugin reference from a name and optional
// marketplace. It is the inverse of ParsePluginRef: an empty marketplace
// yields just the name, otherwise "name@marketplace".
func FormatPluginRef(name, marketplace string) string {
	if marketplace == "" {
		return name
	}
	return name + "@" + marketplace
}

// Install wires a fresh installer to reg/cwd, loads known marketplaces, and
// installs the plugin referenced by ref ("name@marketplace" or "name") into
// scope. It bundles the marketplace-load step every caller needs so call sites
// don't repeat the sequence; cancellation and timeout are the caller's via ctx.
func Install(ctx context.Context, reg *Registry, cwd, ref string, scope Scope) error {
	installer := NewInstaller(reg, cwd)
	if err := installer.LoadMarketplaces(); err != nil {
		return fmt.Errorf("load marketplaces: %w", err)
	}
	return installer.Install(ctx, ref, scope)
}

// Install installs a plugin from a reference.
// Reference format: "plugin-name@marketplace" or "plugin-name" (uses default)
func (i *Installer) Install(ctx context.Context, ref string, scope Scope) error {
	name, marketplace := ParsePluginRef(ref)

	// Find marketplace source
	source, ok := i.marketplaces[marketplace]
	if !ok && marketplace != "" {
		// Fallback: try matching by marketplace.json "name" field
		if resolved := i.resolveMarketplaceByName(marketplace); resolved != "" {
			source, ok = i.marketplaces[resolved]
			marketplace = resolved
		}
		if !ok {
			return fmt.Errorf("unknown marketplace: %s", marketplace)
		}
	}

	// Determine install path
	installDir := i.getInstallDir(scope)
	pluginPath := filepath.Join(installDir, name)

	// Create install directory
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return fmt.Errorf("failed to create install directory: %w", err)
	}

	// Install based on source type
	var err error
	switch source.Type {
	case "directory":
		// Use marketplace manager to find the correct plugin path
		srcPath, pathErr := i.marketplaceManager.GetPluginPath(marketplace, name)
		if pathErr != nil {
			// Fallback to direct path
			srcPath = filepath.Join(source.Path, name)
		}
		err = copyDir(srcPath, pluginPath)
	case "github":
		// First sync the marketplace to get the repo
		if syncErr := i.marketplaceManager.Sync(ctx, marketplace); syncErr != nil {
			return fmt.Errorf("failed to sync marketplace %s: %w", marketplace, syncErr)
		}
		// Then copy the specific plugin subdirectory
		srcPath, pathErr := i.marketplaceManager.GetPluginPath(marketplace, name)
		if pathErr != nil {
			return fmt.Errorf("plugin %s not found in marketplace %s: %w", name, marketplace, pathErr)
		}
		err = copyDir(srcPath, pluginPath)
	default:
		// Try to find in any configured marketplace
		if marketplace == "" {
			for mktID := range i.marketplaces {
				srcPath, pathErr := i.marketplaceManager.GetPluginPath(mktID, name)
				if pathErr == nil {
					err = copyDir(srcPath, pluginPath)
					marketplace = mktID
					break
				}
			}
		}
		if marketplace == "" {
			return fmt.Errorf("could not find plugin: %s", name)
		}
	}

	if err != nil {
		return fmt.Errorf("failed to install plugin: %w", err)
	}

	// Add to installed_plugins.json
	fullName := name
	if marketplace != "" {
		fullName = name + "@" + marketplace
	}
	if err := i.addToInstalled(scope, InstalledPlugin{
		Name:        name,
		Source:      fullName,
		Path:        pluginPath,
		InstalledAt: time.Now().Format(time.RFC3339),
	}); err != nil {
		return err
	}

	// Load the plugin into registry
	plugin, err := LoadPlugin(pluginPath, scope, fullName)
	if err != nil {
		return fmt.Errorf("failed to load installed plugin: %w", err)
	}
	plugin.Enabled = true
	i.registry.Register(plugin)

	// Enable the plugin
	return i.registry.Enable(fullName, scope)
}

// Uninstall removes a plugin.
func (i *Installer) Uninstall(name string, scope Scope) error {
	// Get plugin from registry
	plugin, ok := i.registry.Get(name)
	if !ok {
		return fmt.Errorf("plugin not found: %s", name)
	}

	// Remove plugin directory
	if plugin.Path != "" {
		if err := os.RemoveAll(plugin.Path); err != nil {
			return fmt.Errorf("failed to remove plugin directory: %w", err)
		}
	}

	// Remove from installed_plugins.json
	if err := i.removeFromInstalled(scope, plugin.FullName()); err != nil {
		return err
	}

	// Unregister from registry
	i.registry.Unregister(plugin.FullName())

	return nil
}

// Update updates a plugin to the latest version.
func (i *Installer) Update(ctx context.Context, name string, scope Scope) error {
	plugin, ok := i.registry.Get(name)
	if !ok {
		return fmt.Errorf("plugin not found: %s", name)
	}

	// For git-based plugins, try git pull
	gitDir := filepath.Join(plugin.Path, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		cmd := exec.CommandContext(ctx, "git", "-C", plugin.Path, "pull", "--ff-only")
		return cmd.Run()
	}

	// For directory-based plugins, re-install
	return i.Install(ctx, plugin.FullName(), scope)
}

// getInstallDir returns the installation directory for a scope.
func (i *Installer) getInstallDir(scope Scope) string {
	homeDir, _ := os.UserHomeDir()

	switch scope {
	case ScopeUser:
		return filepath.Join(confdir.Dir(homeDir), "plugins", "cache")
	case ScopeProject:
		return filepath.Join(confdir.Dir(i.cwd), "plugins")
	case ScopeLocal:
		return filepath.Join(confdir.Dir(i.cwd), "plugins-local")
	default:
		return filepath.Join(confdir.Dir(homeDir), "plugins", "cache")
	}
}

// addToInstalled adds a plugin to installed_plugins.json using v2 format.
func (i *Installer) addToInstalled(scope Scope, plugin InstalledPlugin) error {
	return i.addToInstalledV2(scope, plugin.Source, PluginInstallInfo{
		Scope:       string(scope),
		InstallPath: plugin.Path,
		Version:     plugin.Version,
		InstalledAt: plugin.InstalledAt,
		LastUpdated: plugin.InstalledAt,
	})
}

// addToInstalledV2 adds a plugin to installed_plugins.json using v2 format.
func (i *Installer) addToInstalledV2(scope Scope, pluginKey string, info PluginInstallInfo) error {
	installedFile := GetInstalledPluginsFile(i.cwd, scope)
	if err := os.MkdirAll(filepath.Dir(installedFile), 0o755); err != nil {
		return err
	}

	v2 := loadInstalledPluginsV2(installedFile)
	existing := v2.Plugins[pluginKey]

	// Update existing entry for this scope or prepend new one
	updated := false
	for idx, inst := range existing {
		if inst.Scope == info.Scope {
			existing[idx] = info
			updated = true
			break
		}
	}
	if !updated {
		existing = append([]PluginInstallInfo{info}, existing...)
	}
	v2.Plugins[pluginKey] = existing

	data, err := json.MarshalIndent(v2, "", "  ")
	if err != nil {
		return err
	}
	tmp := installedFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, installedFile); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// loadInstalledPluginsV2 loads the installed plugins in v2 format.
func loadInstalledPluginsV2(path string) *InstalledPluginsV2 {
	v2 := &InstalledPluginsV2{
		Version: 2,
		Plugins: make(map[string][]PluginInstallInfo),
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return v2
	}

	// Try v2 format first
	if err := json.Unmarshal(data, v2); err == nil && v2.Version == 2 {
		if v2.Plugins == nil {
			v2.Plugins = make(map[string][]PluginInstallInfo)
		}
		return v2
	}

	// Try v1 format (array of InstalledPlugin)
	var v1 []InstalledPlugin
	if err := json.Unmarshal(data, &v1); err == nil {
		// Convert to v2
		for _, p := range v1 {
			info := PluginInstallInfo{
				Scope:       "user",
				InstallPath: p.Path,
				Version:     p.Version,
				InstalledAt: p.InstalledAt,
			}
			v2.Plugins[p.Source] = append(v2.Plugins[p.Source], info)
		}
	}

	return v2
}

// removeFromInstalled removes a plugin from installed_plugins.json.
func (i *Installer) removeFromInstalled(scope Scope, source string) error {
	installedFile := GetInstalledPluginsFile(i.cwd, scope)

	v2 := loadInstalledPluginsV2(installedFile)

	// Remove the plugin key entirely or just this scope's entry
	if entries, ok := v2.Plugins[source]; ok {
		// Filter out entries for this scope
		var filtered []PluginInstallInfo
		for _, e := range entries {
			if e.Scope != string(scope) {
				filtered = append(filtered, e)
			}
		}
		if len(filtered) == 0 {
			delete(v2.Plugins, source)
		} else {
			v2.Plugins[source] = filtered
		}
	}

	if len(v2.Plugins) == 0 {
		return os.Remove(installedFile)
	}

	data, err := json.MarshalIndent(v2, "", "  ")
	if err != nil {
		return err
	}
	tmp := installedFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, installedFile); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// copyDir copies a directory recursively.
func copyDir(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !srcInfo.IsDir() {
		return fmt.Errorf("source is not a directory: %s", src)
	}

	if err := os.MkdirAll(dst, srcInfo.Mode()); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		// Skip symlinks to prevent symlink escape attacks
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}

		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}

	return nil
}

// copyFile copies a single file.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	return os.WriteFile(dst, data, srcInfo.Mode())
}

// ListInstalled returns all installed plugins for a scope.
// Handles both v1 (JSON array) and v2 (versioned object) formats.
func (i *Installer) ListInstalled(scope Scope) ([]InstalledPlugin, error) {
	installedFile := GetInstalledPluginsFile(i.cwd, scope)

	v2 := loadInstalledPluginsV2(installedFile)

	var installed []InstalledPlugin
	for source, entries := range v2.Plugins {
		for _, info := range entries {
			installed = append(installed, InstalledPlugin{
				Name:        source,
				Source:      source,
				Path:        info.InstallPath,
				Version:     info.Version,
				InstalledAt: info.InstalledAt,
			})
		}
	}
	return installed, nil
}

// GetMarketplaces returns all known marketplaces.
func (i *Installer) GetMarketplaces() []MarketplaceSource {
	result := make([]MarketplaceSource, 0, len(i.marketplaces))
	for _, m := range i.marketplaces {
		result = append(result, m)
	}
	return result
}

// AddMarketplace adds a new marketplace source.
func (i *Installer) AddMarketplace(source MarketplaceSource) error {
	homeDir, _ := os.UserHomeDir()
	path := filepath.Join(confdir.Dir(homeDir), "plugins", "known_marketplaces.json")

	// Load existing
	var km KnownMarketplaces
	if data, err := os.ReadFile(path); err == nil {
		json.Unmarshal(data, &km)
	}

	// Check if already exists
	found := false
	for idx, m := range km.Marketplaces {
		if m.Name == source.Name {
			km.Marketplaces[idx] = source
			found = true
			break
		}
	}

	if !found {
		km.Marketplaces = append(km.Marketplaces, source)
		i.marketplaces[source.Name] = source
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(km, "", "  ")
	if err != nil {
		return err
	}
	// Use atomic tmp+rename to prevent corruption on crash
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
