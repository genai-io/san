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

	"github.com/genai-io/san/internal/atomicfile"
	"github.com/genai-io/san/internal/confdir"
)

// MarketplaceManager manages plugin marketplaces.
type MarketplaceManager struct {
	cwd             string
	marketplaces    map[string]MarketplaceEntry
	configDir       string // Directory for known_marketplaces.json
	cacheDir        string
	marketplacesDir string
}

// NewMarketplaceManager creates a new marketplace manager.
func NewMarketplaceManager(cwd string) *MarketplaceManager {
	homeDir, _ := os.UserHomeDir()
	configDir := filepath.Join(confdir.Dir(homeDir), "plugins")
	return &MarketplaceManager{
		cwd:             cwd,
		marketplaces:    make(map[string]MarketplaceEntry),
		configDir:       configDir,
		cacheDir:        filepath.Join(configDir, "cache"),
		marketplacesDir: filepath.Join(configDir, "marketplaces"),
	}
}

// NewMarketplaceManagerWithConfig creates a marketplace manager with custom config directory.
// Used for testing to avoid modifying user's real configuration.
func NewMarketplaceManagerWithConfig(cwd, configDir string) *MarketplaceManager {
	return &MarketplaceManager{
		cwd:             cwd,
		marketplaces:    make(map[string]MarketplaceEntry),
		configDir:       configDir,
		cacheDir:        filepath.Join(configDir, "cache"),
		marketplacesDir: filepath.Join(configDir, "marketplaces"),
	}
}

// Load loads marketplace configurations from known_marketplaces.json.
func (m *MarketplaceManager) Load() error {
	path := filepath.Join(m.configDir, "known_marketplaces.json")

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	// Try v2 format first (map)
	var v2 map[string]MarketplaceEntry
	if err := json.Unmarshal(data, &v2); err == nil && len(v2) > 0 {
		m.marketplaces = v2
		m.healInstallLocations()
		return nil
	}

	// Fall back to v1 format (array)
	var v1 KnownMarketplaces
	if err := json.Unmarshal(data, &v1); err != nil {
		return err
	}

	// Convert v1 to v2
	for _, ms := range v1.Marketplaces {
		m.marketplaces[ms.Name] = MarketplaceEntry{
			Source: MarketplaceSourceInfo{
				Source: ms.Type,
				Repo:   ms.Repository,
				Path:   ms.Path,
			},
		}
	}

	m.healInstallLocations()
	return nil
}

// healInstallLocations repairs github marketplaces whose recorded
// InstallLocation is empty or stale (e.g. carried over from an older config
// dir whose path no longer exists) by pointing them at the canonical clone
// path. Run once at load so path lookups stay a plain field read rather than
// a stat on every call.
func (m *MarketplaceManager) healInstallLocations() {
	for id, entry := range m.marketplaces {
		if entry.Source.Source != "github" {
			continue
		}
		canonical := filepath.Join(m.marketplacesDir, id)
		if entry.InstallLocation == canonical {
			continue
		}
		if entry.InstallLocation == "" {
			entry.InstallLocation = canonical
			m.marketplaces[id] = entry
			continue
		}
		if _, err := os.Stat(entry.InstallLocation); err != nil {
			if _, err := os.Stat(canonical); err == nil {
				entry.InstallLocation = canonical
				m.marketplaces[id] = entry
			}
		}
	}
}

// Save saves marketplace configurations to known_marketplaces.json.
func (m *MarketplaceManager) Save() error {
	if err := os.MkdirAll(m.configDir, 0o755); err != nil {
		return err
	}

	path := filepath.Join(m.configDir, "known_marketplaces.json")
	return atomicfile.WriteJSON(path, m.marketplaces, 0o644)
}

// Get returns a marketplace by ID.
func (m *MarketplaceManager) Get(id string) (MarketplaceEntry, bool) {
	entry, ok := m.marketplaces[id]
	return entry, ok
}

// List returns all marketplace IDs.
func (m *MarketplaceManager) List() []string {
	ids := make([]string, 0, len(m.marketplaces))
	for id := range m.marketplaces {
		ids = append(ids, id)
	}
	return ids
}

// Add adds a new marketplace.
func (m *MarketplaceManager) Add(id string, entry MarketplaceEntry) error {
	m.marketplaces[id] = entry
	return m.Save()
}

// AddGitHub adds a GitHub-based marketplace.
func (m *MarketplaceManager) AddGitHub(id, repo string) error {
	entry := MarketplaceEntry{
		Source: MarketplaceSourceInfo{
			Source: "github",
			Repo:   repo,
		},
		InstallLocation: filepath.Join(m.marketplacesDir, id),
		LastUpdated:     time.Now().Format(time.RFC3339),
	}
	return m.Add(id, entry)
}

// AddDirectory adds a directory-based marketplace.
func (m *MarketplaceManager) AddDirectory(id, path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	// Validate path exists and is a directory
	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("directory does not exist: %s", absPath)
		}
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("path is not a directory: %s", absPath)
	}

	// Validate it contains plugins or is itself a plugin
	if !isValidMarketplace(absPath) {
		return fmt.Errorf("no plugins found in: %s", absPath)
	}

	entry := MarketplaceEntry{
		Source: MarketplaceSourceInfo{
			Source: "directory",
			Path:   absPath,
		},
		LastUpdated: time.Now().Format(time.RFC3339),
	}
	return m.Add(id, entry)
}

// isValidMarketplace checks if a directory is a valid marketplace.
// A valid marketplace either contains plugins/ subdirectory with plugins,
// or has plugin directories directly inside it.
func isValidMarketplace(path string) bool {
	// Check for plugins/ subdirectory
	pluginsDir := filepath.Join(path, "plugins")
	if entries, err := os.ReadDir(pluginsDir); err == nil {
		for _, e := range entries {
			if e.IsDir() && isValidPlugin(filepath.Join(pluginsDir, e.Name())) {
				return true
			}
		}
	}

	// Check if direct subdirectories are plugins
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() && isValidPlugin(filepath.Join(path, e.Name())) {
			return true
		}
	}

	return false
}

// Remove removes a marketplace.
func (m *MarketplaceManager) Remove(id string) error {
	delete(m.marketplaces, id)
	return m.Save()
}

// Sync synchronizes a marketplace (clone/update from source).
func (m *MarketplaceManager) Sync(ctx context.Context, id string) error {
	entry, ok := m.marketplaces[id]
	if !ok {
		return fmt.Errorf("marketplace not found: %s", id)
	}

	switch entry.Source.Source {
	case "github":
		return m.syncGitHub(ctx, id, entry)
	case "directory":
		// Directory marketplaces don't need syncing
		return nil
	default:
		return fmt.Errorf("unsupported marketplace source: %s", entry.Source.Source)
	}
}

// syncGitHub clones or updates a GitHub marketplace.
func (m *MarketplaceManager) syncGitHub(ctx context.Context, id string, entry MarketplaceEntry) error {
	destPath := entry.InstallLocation
	if destPath == "" {
		destPath = filepath.Join(m.marketplacesDir, id)
	}

	// Check if already cloned
	gitDir := filepath.Join(destPath, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		// Pull updates
		cmd := exec.CommandContext(ctx, "git", "-C", destPath, "pull", "--ff-only")
		return cmd.Run()
	}

	// Clone
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	url := fmt.Sprintf("https://github.com/%s.git", entry.Source.Repo)
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", url, destPath)
	if err := cmd.Run(); err != nil {
		return err
	}

	// Update entry with install location
	entry.InstallLocation = destPath
	entry.LastUpdated = time.Now().Format(time.RFC3339)
	m.marketplaces[id] = entry
	return m.Save()
}

// SyncOrPrune syncs the marketplace and self-heals a broken GitHub source: if
// the sync fails and the marketplace's local clone no longer exists on disk,
// the now-unusable entry is removed. It returns the sync error (if any)
// regardless of whether a prune happened, so callers still surface the failure.
// Pruning is best-effort; a failure to remove is ignored.
//
// Bulk SyncAll and install-time syncs intentionally stay on plain Sync — they
// should not silently remove marketplaces out from under a wider operation.
func (m *MarketplaceManager) SyncOrPrune(ctx context.Context, id string) error {
	err := m.Sync(ctx, id)
	if err == nil {
		return nil
	}
	if entry, ok := m.Get(id); ok && entry.Source.Source == "github" {
		if _, statErr := os.Stat(entry.InstallLocation); os.IsNotExist(statErr) {
			_ = m.Remove(id)
		}
	}
	return err
}

// SyncAll synchronizes all marketplaces.
func (m *MarketplaceManager) SyncAll(ctx context.Context) []error {
	var errors []error
	for id := range m.marketplaces {
		if err := m.Sync(ctx, id); err != nil {
			errors = append(errors, fmt.Errorf("%s: %w", id, err))
		}
	}
	return errors
}

// GetPluginPath returns the path to a plugin in a marketplace.
func (m *MarketplaceManager) GetPluginPath(marketplaceID, pluginName string) (string, error) {
	basePath, err := m.getMarketplaceBasePath(marketplaceID)
	if err != nil {
		return "", err
	}

	searchPaths := []string{
		filepath.Join(basePath, "plugins", pluginName),
		filepath.Join(basePath, pluginName),
		filepath.Join(basePath, "Claude", "plugins", pluginName),
	}

	for _, path := range searchPaths {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	return "", fmt.Errorf("plugin not found: %s in marketplace %s", pluginName, marketplaceID)
}

// getMarketplaceBasePath returns the base path for a marketplace.
func (m *MarketplaceManager) getMarketplaceBasePath(marketplaceID string) (string, error) {
	entry, ok := m.marketplaces[marketplaceID]
	if !ok {
		return "", fmt.Errorf("marketplace not found: %s", marketplaceID)
	}

	switch entry.Source.Source {
	case "github":
		// InstallLocation is normalized at load (see healInstallLocations);
		// fall back to the canonical path only if it was never recorded.
		if entry.InstallLocation != "" {
			return entry.InstallLocation, nil
		}
		return filepath.Join(m.marketplacesDir, marketplaceID), nil
	case "directory":
		return entry.Source.Path, nil
	default:
		return "", fmt.Errorf("unsupported marketplace source: %s", entry.Source.Source)
	}
}

// MarketplacePlugins returns the plugins a marketplace offers. It prefers the
// marketplace.json manifest — Claude Code's model, where plugins are declared
// and each carries its own source (which may point to another repo). When a
// marketplace has no manifest plugins[], it falls back to scanning the repo for
// vendored plugin subdirectories so directory-style marketplaces keep working.
func (m *MarketplaceManager) MarketplacePlugins(marketplaceID string) ([]MarketplacePlugin, error) {
	if meta, err := m.GetMarketplaceMetadata(marketplaceID); err == nil && len(meta.Plugins) > 0 {
		return meta.Plugins, nil
	}

	names, err := m.ListPlugins(marketplaceID)
	if err != nil {
		return nil, err
	}
	plugins := make([]MarketplacePlugin, 0, len(names))
	for _, name := range names {
		entry := MarketplacePlugin{
			Name:   name,
			Source: PluginSource{Type: SourcePath, Path: name},
		}
		if path, perr := m.GetPluginPath(marketplaceID, name); perr == nil {
			if man, merr := loadManifest(path); merr == nil {
				entry.Description = man.Description
				entry.Version = man.Version
				entry.Author = man.Author
			}
		}
		plugins = append(plugins, entry)
	}
	return plugins, nil
}

// safeRelPath cleans a relative path and rejects absolute paths or any that
// escape their base via "..". The returned path is safe to filepath.Join onto
// a trusted base. Used to confine plugin sources to the directory they name.
func safeRelPath(p string) (string, error) {
	clean := filepath.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.IsAbs(clean) {
		return "", fmt.Errorf("invalid path: %s", p)
	}
	return clean, nil
}

// ResolveLocalPluginPath returns the on-disk path of a plugin whose content
// lives inside the marketplace repo: a declared relative "path" source, or —
// as a fallback — a vendored subdirectory found by name.
func (m *MarketplaceManager) ResolveLocalPluginPath(marketplaceID, name string, src PluginSource) (string, error) {
	if src.Type == SourcePath && src.Path != "" {
		base, err := m.getMarketplaceBasePath(marketplaceID)
		if err != nil {
			return "", err
		}
		clean, err := safeRelPath(src.Path)
		if err != nil {
			return "", err
		}
		full := filepath.Join(base, clean)
		if _, err := os.Stat(full); err == nil {
			return full, nil
		}
	}
	return m.GetPluginPath(marketplaceID, name)
}

// ListPlugins returns the names of vendored plugin subdirectories in a
// marketplace. It scans the repo layout and does not read marketplace.json;
// use MarketplacePlugins for the manifest-aware, declared plugin list.
func (m *MarketplaceManager) ListPlugins(marketplaceID string) ([]string, error) {
	basePath, err := m.getMarketplaceBasePath(marketplaceID)
	if err != nil {
		return nil, err
	}

	searchDirs := []string{
		filepath.Join(basePath, "plugins"),
		basePath,
		filepath.Join(basePath, "Claude", "plugins"),
	}

	var plugins []string
	seen := make(map[string]bool)

	for _, dir := range searchDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			// Skip non-directories, already seen, and hidden directories
			if !e.IsDir() || seen[name] || strings.HasPrefix(name, ".") {
				continue
			}
			pluginPath := filepath.Join(dir, name)
			if isValidPlugin(pluginPath) {
				plugins = append(plugins, name)
				seen[name] = true
			}
		}
	}

	return plugins, nil
}

// isValidPlugin checks if a directory contains a valid plugin.
func isValidPlugin(path string) bool {
	// Check for .san-plugin or .claude-plugin manifest
	for _, metaDir := range []string{SanPluginDir, ClaudePluginDir} {
		manifestPath := filepath.Join(path, metaDir, "plugin.json")
		if _, err := os.Stat(manifestPath); err == nil {
			return true
		}
	}
	// Also check for skills, commands, or agents directories
	for _, dir := range []string{"skills", "commands", "agents"} {
		if _, err := os.Stat(filepath.Join(path, dir)); err == nil {
			return true
		}
	}
	return false
}

// GetMarketplaceMetadata loads the marketplace.json metadata from a marketplace.
func (m *MarketplaceManager) GetMarketplaceMetadata(marketplaceID string) (*MarketplaceMetadata, error) {
	basePath, err := m.getMarketplaceBasePath(marketplaceID)
	if err != nil {
		return nil, err
	}

	searchPaths := []string{
		filepath.Join(basePath, ClaudePluginDir, "marketplace.json"),
		filepath.Join(basePath, SanPluginDir, "marketplace.json"),
		filepath.Join(basePath, "marketplace.json"),
	}

	for _, path := range searchPaths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var meta MarketplaceMetadata
		if err := json.Unmarshal(data, &meta); err != nil {
			continue
		}
		return &meta, nil
	}

	return nil, fmt.Errorf("no marketplace metadata found for %s", marketplaceID)
}
