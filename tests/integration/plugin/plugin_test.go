package plugin_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/genai-io/san/internal/plugin"
)

var testPluginDir string

func init() {
	// Find test plugin directory
	cwd, _ := os.Getwd()
	// Navigate up to find the testdata directory
	for range 5 {
		testDir := filepath.Join(cwd, "testdata", "test-plugin")
		if _, err := os.Stat(testDir); err == nil {
			testPluginDir = testDir
			break
		}
		cwd = filepath.Dir(cwd)
	}
}

func TestPluginLoading(t *testing.T) {
	if testPluginDir == "" {
		t.Skip("Test plugin directory not found")
	}

	p, err := plugin.LoadPlugin(testPluginDir, plugin.ScopeLocal, "test-plugin")
	if err != nil {
		t.Fatalf("LoadPlugin() error = %v", err)
	}

	// Verify manifest
	if p.Manifest.Name != "test-plugin" {
		t.Errorf("Name = %q, want %q", p.Manifest.Name, "test-plugin")
	}
	if p.Manifest.Version != "1.0.0" {
		t.Errorf("Version = %q, want %q", p.Manifest.Version, "1.0.0")
	}
	if p.Manifest.Description == "" {
		t.Error("Description should not be empty")
	}
}

func TestPluginComponents(t *testing.T) {
	if testPluginDir == "" {
		t.Skip("Test plugin directory not found")
	}

	p, err := plugin.LoadPlugin(testPluginDir, plugin.ScopeLocal, "test-plugin")
	if err != nil {
		t.Fatalf("LoadPlugin() error = %v", err)
	}

	// Check skills
	if len(p.Components.Skills) != 1 {
		t.Errorf("Skills count = %d, want 1", len(p.Components.Skills))
	}

	// Check agents
	if len(p.Components.Agents) != 1 {
		t.Errorf("Agents count = %d, want 1", len(p.Components.Agents))
	}

	// Check commands
	if len(p.Components.Commands) != 1 {
		t.Errorf("Commands count = %d, want 1", len(p.Components.Commands))
	}

	// Check hooks
	if p.Components.Hooks == nil {
		t.Error("Hooks should not be nil")
	} else if len(p.Components.Hooks.Hooks) != 1 {
		t.Errorf("Hooks events count = %d, want 1", len(p.Components.Hooks.Hooks))
	}
}

func TestPluginValidation(t *testing.T) {
	if testPluginDir == "" {
		t.Skip("Test plugin directory not found")
	}

	err := plugin.ValidatePlugin(testPluginDir)
	if err != nil {
		t.Errorf("ValidatePlugin() error = %v", err)
	}
}

func TestRegistryWithPlugin(t *testing.T) {
	if testPluginDir == "" {
		t.Skip("Test plugin directory not found")
	}

	registry := plugin.NewRegistry()
	ctx := context.Background()

	// Load plugin
	err := registry.LoadFromPath(ctx, testPluginDir)
	if err != nil {
		t.Fatalf("LoadFromPath() error = %v", err)
	}

	// Verify it's loaded
	if registry.Count() != 1 {
		t.Errorf("Count() = %d, want 1", registry.Count())
	}

	// Get the plugin
	p, ok := registry.Get("test-plugin")
	if !ok {
		t.Fatal("Plugin not found in registry")
	}

	// Plugins loaded via path should be enabled
	if !p.Enabled {
		t.Error("Plugin should be enabled")
	}
}

func TestRegistryLoad_AppliesEnabledStateFromSettings(t *testing.T) {
	tmpHome := t.TempDir()
	tmpCwd := t.TempDir()
	t.Setenv("HOME", tmpHome)

	pluginDir := filepath.Join(tmpHome, ".gen", "plugins", "test-plugin")
	manifestDir := filepath.Join(pluginDir, ".gen-plugin")
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(plugin manifest): %v", err)
	}
	manifest := map[string]any{
		"name":    "test-plugin",
		"version": "1.0.0",
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("Marshal(manifest): %v", err)
	}
	if err := os.WriteFile(filepath.Join(manifestDir, "plugin.json"), data, 0o644); err != nil {
		t.Fatalf("WriteFile(manifest): %v", err)
	}

	settingsPath := filepath.Join(tmpHome, ".gen", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(settings): %v", err)
	}
	settings := map[string]any{
		"enabledPlugins": map[string]bool{
			"test-plugin": false,
		},
	}
	settingsData, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("Marshal(settings): %v", err)
	}
	if err := os.WriteFile(settingsPath, settingsData, 0o644); err != nil {
		t.Fatalf("WriteFile(settings): %v", err)
	}

	registry := plugin.NewRegistry()
	if err := registry.Load(context.Background(), tmpCwd); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	p, ok := registry.Get("test-plugin")
	if !ok {
		t.Fatal("expected plugin loaded from user plugin dir")
	}
	if p.Enabled {
		t.Fatal("expected settings.json to disable the plugin")
	}
}

func TestClaudeCodeCompatibility(t *testing.T) {
	if testPluginDir == "" {
		t.Skip("Test plugin directory not found")
	}

	// Check both manifests exist and are valid
	genManifest := filepath.Join(testPluginDir, ".gen-plugin", "plugin.json")
	claudeManifest := filepath.Join(testPluginDir, ".claude-plugin", "plugin.json")

	// Load and compare manifests
	var genData, claudeData map[string]any

	genContent, err := os.ReadFile(genManifest)
	if err != nil {
		t.Fatalf("Failed to read San manifest: %v", err)
	}
	if err := json.Unmarshal(genContent, &genData); err != nil {
		t.Fatalf("Failed to parse San manifest: %v", err)
	}

	claudeContent, err := os.ReadFile(claudeManifest)
	if err != nil {
		t.Fatalf("Failed to read Claude Code manifest: %v", err)
	}
	if err := json.Unmarshal(claudeContent, &claudeData); err != nil {
		t.Fatalf("Failed to parse Claude Code manifest: %v", err)
	}

	// Verify they have the same core fields
	if genData["name"] != claudeData["name"] {
		t.Errorf("Name mismatch: San=%v, Claude=%v", genData["name"], claudeData["name"])
	}
	if genData["version"] != claudeData["version"] {
		t.Errorf("Version mismatch: San=%v, Claude=%v", genData["version"], claudeData["version"])
	}
}

func TestInstalledPluginsV2Format(t *testing.T) {
	// Create a temp directory
	tmpDir := t.TempDir()

	// Create a v2 format file
	v2Data := plugin.InstalledPluginsV2{
		Version: 2,
		Plugins: map[string][]plugin.PluginInstallInfo{
			"test-plugin@test-marketplace": {
				{
					Scope:        "user",
					InstallPath:  "/path/to/plugin",
					Version:      "1.0.0",
					InstalledAt:  "2024-01-01T00:00:00Z",
					LastUpdated:  "2024-01-01T00:00:00Z",
					GitCommitSha: "abc123",
				},
			},
		},
	}

	installedFile := filepath.Join(tmpDir, "installed_plugins.json")
	data, err := json.MarshalIndent(v2Data, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal v2 data: %v", err)
	}
	if err := os.WriteFile(installedFile, data, 0o644); err != nil {
		t.Fatalf("Failed to write installed_plugins.json: %v", err)
	}

	// Verify the file can be loaded
	content, err := os.ReadFile(installedFile)
	if err != nil {
		t.Fatalf("Failed to read installed_plugins.json: %v", err)
	}

	var loaded plugin.InstalledPluginsV2
	if err := json.Unmarshal(content, &loaded); err != nil {
		t.Fatalf("Failed to unmarshal v2 data: %v", err)
	}

	if loaded.Version != 2 {
		t.Errorf("Version = %d, want 2", loaded.Version)
	}
	if len(loaded.Plugins) != 1 {
		t.Errorf("Plugins count = %d, want 1", len(loaded.Plugins))
	}

	entries := loaded.Plugins["test-plugin@test-marketplace"]
	if len(entries) != 1 {
		t.Errorf("Entries count = %d, want 1", len(entries))
	}
	if entries[0].GitCommitSha != "abc123" {
		t.Errorf("GitCommitSha = %q, want %q", entries[0].GitCommitSha, "abc123")
	}
}

func TestMarketplaceManager(t *testing.T) {
	if testPluginDir == "" {
		t.Skip("Test plugin directory not found")
	}

	tmpDir := t.TempDir()
	// Use custom config dir to avoid modifying user's real configuration
	manager := plugin.NewMarketplaceManagerWithConfig(tmpDir, tmpDir)

	// Use the parent directory of the test plugin (which contains test-plugin/)
	pluginsParentDir := filepath.Dir(testPluginDir)

	// Add a directory marketplace
	err := manager.AddDirectory("test-local", pluginsParentDir)
	if err != nil {
		t.Fatalf("AddDirectory() error = %v", err)
	}

	// List marketplaces
	ids := manager.List()
	if len(ids) != 1 {
		t.Errorf("List() length = %d, want 1", len(ids))
	}

	// Get marketplace
	entry, ok := manager.Get("test-local")
	if !ok {
		t.Fatal("Marketplace not found")
	}
	if entry.Source.Source != "directory" {
		t.Errorf("Source = %q, want %q", entry.Source.Source, "directory")
	}

	// Remove marketplace
	err = manager.Remove("test-local")
	if err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	ids = manager.List()
	if len(ids) != 0 {
		t.Errorf("After remove, List() length = %d, want 0", len(ids))
	}
}

func TestPluginAgentPaths(t *testing.T) {
	if testPluginDir == "" {
		t.Skip("Test plugin directory not found")
	}

	// Create a fresh registry
	registry := plugin.NewRegistry()
	ctx := context.Background()

	// Load plugin
	err := registry.LoadFromPath(ctx, testPluginDir)
	if err != nil {
		t.Fatalf("LoadFromPath() error = %v", err)
	}

	// Get enabled plugins
	enabled := registry.GetEnabled()
	if len(enabled) != 1 {
		t.Fatalf("Expected 1 enabled plugin, got %d", len(enabled))
	}

	// Check that agent paths are resolved
	p := enabled[0]
	if len(p.Components.Agents) != 1 {
		t.Errorf("Expected 1 agent path, got %d", len(p.Components.Agents))
	}

	// Verify the agent file exists
	if len(p.Components.Agents) > 0 {
		agentPath := p.Components.Agents[0]
		if _, err := os.Stat(agentPath); os.IsNotExist(err) {
			t.Errorf("Agent file does not exist: %s", agentPath)
		}
		t.Logf("Agent path: %s", agentPath)
	}
}
