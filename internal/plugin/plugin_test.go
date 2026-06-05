package plugin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandPluginRoot(t *testing.T) {
	tests := []struct {
		input      string
		pluginPath string
		expected   string
	}{
		{
			input:      "${SAN_PLUGIN_ROOT}/scripts/test.sh",
			pluginPath: "/home/user/plugins/myplugin",
			expected:   "/home/user/plugins/myplugin/scripts/test.sh",
		},
		{
			input:      "${CLAUDE_PLUGIN_ROOT}/config.json",
			pluginPath: "/tmp/plugin",
			expected:   "/tmp/plugin/config.json",
		},
		{
			input:      "no-variables.txt",
			pluginPath: "/tmp/plugin",
			expected:   "no-variables.txt",
		},
	}

	for _, tt := range tests {
		result := ExpandPluginRoot(tt.input, tt.pluginPath)
		if result != tt.expected {
			t.Errorf("ExpandPluginRoot(%q, %q) = %q, want %q",
				tt.input, tt.pluginPath, result, tt.expected)
		}
	}
}

func TestParsePluginRef(t *testing.T) {
	tests := []struct {
		ref        string
		wantName   string
		wantMarket string
	}{
		{"git@my-plugins", "git", "my-plugins"},
		{"git", "git", ""},
		{"deployment-tools@enterprise", "deployment-tools", "enterprise"},
	}

	for _, tt := range tests {
		name, market := ParsePluginRef(tt.ref)
		if name != tt.wantName || market != tt.wantMarket {
			t.Errorf("ParsePluginRef(%q) = (%q, %q), want (%q, %q)",
				tt.ref, name, market, tt.wantName, tt.wantMarket)
		}
	}
}

func TestScope(t *testing.T) {
	tests := []struct {
		scope Scope
		str   string
		icon  string
	}{
		{ScopeUser, "user", "👤"},
		{ScopeProject, "project", "📁"},
		{ScopeLocal, "local", "💻"},
		{ScopeManaged, "managed", "🔒"},
	}

	for _, tt := range tests {
		if tt.scope.String() != tt.str {
			t.Errorf("Scope(%q).String() = %q, want %q", tt.scope, tt.scope.String(), tt.str)
		}
		if tt.scope.Icon() != tt.icon {
			t.Errorf("Scope(%q).Icon() = %q, want %q", tt.scope, tt.scope.Icon(), tt.icon)
		}
	}
}

func TestLoadPlugin(t *testing.T) {
	// Create a temporary plugin directory
	tmpDir := t.TempDir()

	// Create .san-plugin/plugin.json
	pluginMetaDir := filepath.Join(tmpDir, ".san-plugin")
	if err := os.MkdirAll(pluginMetaDir, 0o755); err != nil {
		t.Fatal(err)
	}

	manifest := Manifest{
		Name:        "test-plugin",
		Version:     "1.0.0",
		Description: "A test plugin",
	}
	manifestJSON, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(pluginMetaDir, "plugin.json"), manifestJSON, 0o644); err != nil {
		t.Fatal(err)
	}

	// Create skills directory with a skill
	skillsDir := filepath.Join(tmpDir, "skills", "hello")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillContent := `---
name: hello
description: A greeting skill
---
Say hello!
`
	if err := os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte(skillContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create agents directory
	agentsDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	agentContent := `---
name: test-agent
description: A test agent
---
You are a test agent.
`
	if err := os.WriteFile(filepath.Join(agentsDir, "test-agent.md"), []byte(agentContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Load the plugin
	plugin, err := LoadPlugin(tmpDir, ScopeUser, "test-plugin@test")
	if err != nil {
		t.Fatalf("LoadPlugin() error = %v", err)
	}

	// Verify manifest
	if plugin.Manifest.Name != "test-plugin" {
		t.Errorf("Plugin name = %q, want %q", plugin.Manifest.Name, "test-plugin")
	}
	if plugin.Manifest.Version != "1.0.0" {
		t.Errorf("Plugin version = %q, want %q", plugin.Manifest.Version, "1.0.0")
	}

	// Verify scope and source
	if plugin.Scope != ScopeUser {
		t.Errorf("Plugin scope = %v, want %v", plugin.Scope, ScopeUser)
	}
	if plugin.Source != "test-plugin@test" {
		t.Errorf("Plugin source = %q, want %q", plugin.Source, "test-plugin@test")
	}

	// Verify components were resolved
	if len(plugin.Components.Skills) != 1 {
		t.Errorf("Plugin skills count = %d, want 1", len(plugin.Components.Skills))
	}
	if len(plugin.Components.Agents) != 1 {
		t.Errorf("Plugin agents count = %d, want 1", len(plugin.Components.Agents))
	}
}

func TestRegistry(t *testing.T) {
	// Create a test plugin
	tmpDir := t.TempDir()

	pluginMetaDir := filepath.Join(tmpDir, ".san-plugin")
	if err := os.MkdirAll(pluginMetaDir, 0o755); err != nil {
		t.Fatal(err)
	}

	manifest := Manifest{Name: "registry-test"}
	manifestJSON, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(pluginMetaDir, "plugin.json"), manifestJSON, 0o644); err != nil {
		t.Fatal(err)
	}

	// Create and register plugin
	registry := NewRegistry()
	plugin, _ := LoadPlugin(tmpDir, ScopeUser, "registry-test")
	plugin.Enabled = true
	registry.Register(plugin)

	// Test Get
	got, ok := registry.Get("registry-test")
	if !ok {
		t.Error("Registry.Get() did not find plugin")
	}
	if got.Name() != "registry-test" {
		t.Errorf("Registry.Get() name = %q, want %q", got.Name(), "registry-test")
	}

	// Test List
	list := registry.List()
	if len(list) != 1 {
		t.Errorf("Registry.List() length = %d, want 1", len(list))
	}

	// Test GetEnabled
	enabled := registry.GetEnabled()
	if len(enabled) != 1 {
		t.Errorf("Registry.GetEnabled() length = %d, want 1", len(enabled))
	}

	// Test Count
	if registry.Count() != 1 {
		t.Errorf("Registry.Count() = %d, want 1", registry.Count())
	}

	// Test EnabledCount
	if registry.EnabledCount() != 1 {
		t.Errorf("Registry.EnabledCount() = %d, want 1", registry.EnabledCount())
	}

	// Test Unregister
	registry.Unregister("registry-test")
	if registry.Count() != 0 {
		t.Errorf("After Unregister, Count() = %d, want 0", registry.Count())
	}
}

func TestRegistry_GetMatchesMarketplacePluginByShortName(t *testing.T) {
	registry := NewRegistry()
	registry.Register(&Plugin{
		Manifest: Manifest{Name: "deploy"},
		Source:   "deploy@corp",
	})

	got, ok := registry.Get("deploy")
	if !ok {
		t.Fatal("expected short-name lookup to resolve marketplace plugin")
	}
	if got.FullName() != "deploy@corp" {
		t.Fatalf("Registry.Get(short) resolved %q, want %q", got.FullName(), "deploy@corp")
	}
}

func TestRegistry_EnableDisable_PersistsScopedSettings(t *testing.T) {
	tmpHome := t.TempDir()
	tmpCwd := t.TempDir()
	t.Setenv("HOME", tmpHome)

	tests := []struct {
		name         string
		scope        Scope
		enable       bool
		settingsPath string
	}{
		{
			name:         "user enable",
			scope:        ScopeUser,
			enable:       true,
			settingsPath: filepath.Join(tmpHome, ".san", "settings.json"),
		},
		{
			name:         "project disable",
			scope:        ScopeProject,
			enable:       false,
			settingsPath: filepath.Join(tmpCwd, ".san", "settings.json"),
		},
		{
			name:         "local disable",
			scope:        ScopeLocal,
			enable:       false,
			settingsPath: filepath.Join(tmpCwd, ".san", "settings.local.json"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := NewRegistry()
			registry.cwd = tmpCwd
			registry.Register(&Plugin{
				Manifest: Manifest{Name: "deploy"},
				Source:   "deploy@corp",
				Enabled:  !tt.enable,
			})

			if err := os.MkdirAll(filepath.Dir(tt.settingsPath), 0o755); err != nil {
				t.Fatalf("MkdirAll(settings dir): %v", err)
			}
			seed := map[string]any{
				"theme": "night",
				"enabledPlugins": map[string]any{
					"other@corp": true,
				},
			}
			data, err := json.Marshal(seed)
			if err != nil {
				t.Fatalf("Marshal(seed): %v", err)
			}
			if err := os.WriteFile(tt.settingsPath, data, 0o644); err != nil {
				t.Fatalf("WriteFile(seed settings): %v", err)
			}

			var opErr error
			if tt.enable {
				opErr = registry.Enable("deploy@corp", tt.scope)
			} else {
				opErr = registry.Disable("deploy@corp", tt.scope)
			}
			if opErr != nil {
				t.Fatalf("registry toggle failed: %v", opErr)
			}

			toggled, ok := registry.Get("deploy@corp")
			if !ok {
				t.Fatal("expected plugin to remain registered")
			}
			if toggled.Enabled != tt.enable {
				t.Fatalf("plugin enabled state = %v, want %v", toggled.Enabled, tt.enable)
			}

			saved, err := os.ReadFile(tt.settingsPath)
			if err != nil {
				t.Fatalf("ReadFile(saved settings): %v", err)
			}

			var settings struct {
				Theme          string         `json:"theme"`
				EnabledPlugins map[string]any `json:"enabledPlugins"`
			}
			if err := json.Unmarshal(saved, &settings); err != nil {
				t.Fatalf("Unmarshal(saved settings): %v", err)
			}
			if settings.Theme != "night" {
				t.Fatalf("existing settings should be preserved, got theme %q", settings.Theme)
			}
			if got := settings.EnabledPlugins["deploy@corp"]; got != tt.enable {
				t.Fatalf("enabledPlugins[%q] = %v, want %v", "deploy@corp", got, tt.enable)
			}
			if got := settings.EnabledPlugins["other@corp"]; got != true {
				t.Fatalf("existing plugin state lost, got %v", got)
			}
		})
	}
}

func writeTestPlugin(t *testing.T, root, name, version, description string, extraFiles map[string]string) {
	t.Helper()

	metaDir := filepath.Join(root, ".san-plugin")
	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(plugin meta): %v", err)
	}

	manifest := Manifest{
		Name:        name,
		Version:     version,
		Description: description,
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("Marshal(manifest): %v", err)
	}
	if err := os.WriteFile(filepath.Join(metaDir, "plugin.json"), data, 0o644); err != nil {
		t.Fatalf("WriteFile(manifest): %v", err)
	}

	for rel, content := range extraFiles {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%s): %v", rel, err)
		}
	}
}

func TestInstaller_InstallAndUninstall_FromMarketplaceDirectory(t *testing.T) {
	tmpHome := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", tmpHome)

	marketRoot := filepath.Join(t.TempDir(), "market")
	pluginRoot := filepath.Join(marketRoot, "deploy")
	writeTestPlugin(t, pluginRoot, "deploy", "1.2.3", "Deploy plugin", map[string]string{
		"skills/deploy/SKILL.md": "---\nname: deploy\ndescription: Deploy skill\n---\nship it\n",
	})

	registry := NewRegistry()
	installer := NewInstaller(registry, cwd)
	if err := installer.marketplaceManager.AddDirectory("local-market", marketRoot); err != nil {
		t.Fatalf("AddDirectory() error: %v", err)
	}
	if err := installer.LoadMarketplaces(); err != nil {
		t.Fatalf("LoadMarketplaces() error: %v", err)
	}

	if err := installer.Install(context.Background(), "deploy@local-market", ScopeProject); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	installPath := filepath.Join(cwd, ".san", "plugins", "deploy")
	if _, err := os.Stat(installPath); err != nil {
		t.Fatalf("expected installed plugin dir: %v", err)
	}

	installedFile := GetInstalledPluginsFile(cwd, ScopeProject)
	data, err := os.ReadFile(installedFile)
	if err != nil {
		t.Fatalf("ReadFile(installed_plugins): %v", err)
	}
	if !strings.Contains(string(data), "deploy@local-market") {
		t.Fatalf("installed_plugins.json missing installed plugin entry: %s", string(data))
	}

	p, ok := registry.Get("deploy@local-market")
	if !ok {
		t.Fatal("expected installed plugin registered")
	}
	if !p.Enabled {
		t.Fatal("expected installed plugin enabled")
	}

	if err := installer.Uninstall("deploy@local-market", ScopeProject); err != nil {
		t.Fatalf("Uninstall() error: %v", err)
	}
	if _, err := os.Stat(installPath); !os.IsNotExist(err) {
		t.Fatalf("expected plugin dir removed, stat err=%v", err)
	}
	if _, ok := registry.Get("deploy@local-market"); ok {
		t.Fatal("expected plugin to be removed from registry")
	}
	if _, err := os.Stat(installedFile); !os.IsNotExist(err) {
		t.Fatalf("expected installed_plugins.json removed when empty, stat err=%v", err)
	}
}

func TestFormatPluginRef(t *testing.T) {
	if got := FormatPluginRef("git", "my-market"); got != "git@my-market" {
		t.Fatalf("FormatPluginRef with marketplace = %q, want git@my-market", got)
	}
	if got := FormatPluginRef("git", ""); got != "git" {
		t.Fatalf("FormatPluginRef without marketplace = %q, want git", got)
	}
	// Round-trips with ParsePluginRef.
	if name, market := ParsePluginRef(FormatPluginRef("deploy", "local-market")); name != "deploy" || market != "local-market" {
		t.Fatalf("round-trip = (%q, %q), want (deploy, local-market)", name, market)
	}
}

// TestInstall_LoadsMarketplacesAndInstalls covers the package-level Install
// helper, which both the plugin overlay and the /plugin install command call.
// It must load known marketplaces itself before installing.
func TestInstall_LoadsMarketplacesAndInstalls(t *testing.T) {
	tmpHome := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", tmpHome)

	marketRoot := filepath.Join(t.TempDir(), "market")
	pluginRoot := filepath.Join(marketRoot, "deploy")
	writeTestPlugin(t, pluginRoot, "deploy", "1.2.3", "Deploy plugin", map[string]string{
		"skills/deploy/SKILL.md": "---\nname: deploy\ndescription: Deploy skill\n---\nship it\n",
	})

	// Persist the marketplace to disk so the installer that Install() builds
	// internally rediscovers it through LoadMarketplaces.
	mgr := NewMarketplaceManager(cwd)
	if err := mgr.AddDirectory("local-market", marketRoot); err != nil {
		t.Fatalf("AddDirectory() error: %v", err)
	}

	registry := NewRegistry()
	if err := Install(context.Background(), registry, cwd, "deploy@local-market", ScopeProject); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	installPath := filepath.Join(cwd, ".san", "plugins", "deploy")
	if _, err := os.Stat(installPath); err != nil {
		t.Fatalf("expected installed plugin dir: %v", err)
	}
	if _, ok := registry.Get("deploy@local-market"); !ok {
		t.Fatal("expected installed plugin registered")
	}
}

func TestRegistry_LoadScopeMergePrefersLocalOverProjectOverUser(t *testing.T) {
	tmpHome := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", tmpHome)

	writeTestPlugin(t, filepath.Join(tmpHome, ".san", "plugins", "shared"), "shared", "1.0.0", "user plugin", nil)
	writeTestPlugin(t, filepath.Join(cwd, ".san", "plugins", "shared"), "shared", "1.0.0", "project plugin", nil)
	writeTestPlugin(t, filepath.Join(cwd, ".san", "plugins-local", "shared"), "shared", "1.0.0", "local plugin", nil)

	registry := NewRegistry()
	if err := registry.Load(context.Background(), cwd); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	p, ok := registry.Get("shared")
	if !ok {
		t.Fatal("expected merged plugin to be present")
	}
	if p.Scope != ScopeLocal {
		t.Fatalf("expected local scope to win, got %v", p.Scope)
	}
	if p.Manifest.Description != "local plugin" {
		t.Fatalf("expected local plugin manifest to win, got %q", p.Manifest.Description)
	}
}

func TestPlugin_LSPLoading(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestPlugin(t, tmpDir, "lsp-plugin", "1.0.0", "LSP plugin", map[string]string{
		"lsp.json": `{
  "go": {
    "command": "gopls",
    "args": ["serve"],
    "extensionToLanguage": {
      ".go": "go"
    }
  }
}`,
	})

	manifestPath := filepath.Join(tmpDir, ".san-plugin", "plugin.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("ReadFile(manifest): %v", err)
	}

	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("Unmarshal(manifest): %v", err)
	}
	manifest["lspServers"] = "lsp.json"
	updated, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("Marshal(updated manifest): %v", err)
	}
	if err := os.WriteFile(manifestPath, updated, 0o644); err != nil {
		t.Fatalf("WriteFile(updated manifest): %v", err)
	}

	p, err := LoadPlugin(tmpDir, ScopeLocal, "lsp-plugin")
	if err != nil {
		t.Fatalf("LoadPlugin() error: %v", err)
	}

	server, ok := p.Components.LSP["go"]
	if !ok {
		t.Fatal("expected go LSP server to be loaded")
	}
	if server.Command != "gopls" {
		t.Fatalf("expected gopls command, got %q", server.Command)
	}
	if len(server.Args) != 1 || server.Args[0] != "serve" {
		t.Fatalf("unexpected LSP args: %#v", server.Args)
	}
	if server.ExtensionToLanguage[".go"] != "go" {
		t.Fatalf("expected extension mapping for .go, got %#v", server.ExtensionToLanguage)
	}
}

func TestValidatePlugin(t *testing.T) {
	// Valid plugin
	tmpDir := t.TempDir()
	pluginMetaDir := filepath.Join(tmpDir, ".san-plugin")
	os.MkdirAll(pluginMetaDir, 0o755)

	manifest := Manifest{Name: "valid-plugin", Version: "1.0.0"}
	manifestJSON, _ := json.Marshal(manifest)
	os.WriteFile(filepath.Join(pluginMetaDir, "plugin.json"), manifestJSON, 0o644)

	if err := ValidatePlugin(tmpDir); err != nil {
		t.Errorf("ValidatePlugin() unexpected error = %v", err)
	}

	// Invalid plugin (no manifest)
	emptyDir := t.TempDir()
	if err := ValidatePlugin(emptyDir); err == nil {
		t.Error("ValidatePlugin() expected error for missing manifest")
	}

	// Invalid plugin (no name)
	noNameDir := t.TempDir()
	noNameMetaDir := filepath.Join(noNameDir, ".san-plugin")
	os.MkdirAll(noNameMetaDir, 0o755)
	noNameManifest := Manifest{Version: "1.0.0"} // Missing name
	noNameJSON, _ := json.Marshal(noNameManifest)
	os.WriteFile(filepath.Join(noNameMetaDir, "plugin.json"), noNameJSON, 0o644)

	if err := ValidatePlugin(noNameDir); err == nil {
		t.Error("ValidatePlugin() expected error for missing name")
	}
}

func TestPlugin_Validate_InvalidManifest(t *testing.T) {
	tests := []struct {
		name        string
		manifestFn  func(dir string) error
		expectError bool
		desc        string
	}{
		{
			name: "missing_name",
			manifestFn: func(dir string) error {
				metaDir := filepath.Join(dir, ".san-plugin")
				os.MkdirAll(metaDir, 0o755)
				m := Manifest{Version: "1.0.0"} // Name empty
				data, _ := json.Marshal(m)
				return os.WriteFile(filepath.Join(metaDir, "plugin.json"), data, 0o644)
			},
			expectError: true,
			desc:        "manifest missing 'name' field should fail validation",
		},
		{
			name: "no_manifest_file",
			manifestFn: func(dir string) error {
				// Don't create any manifest
				return nil
			},
			expectError: true,
			desc:        "missing manifest file should fail validation",
		},
		{
			name: "invalid_semver",
			manifestFn: func(dir string) error {
				metaDir := filepath.Join(dir, ".san-plugin")
				os.MkdirAll(metaDir, 0o755)
				m := Manifest{Name: "my-plugin", Version: "not-semver"}
				data, _ := json.Marshal(m)
				return os.WriteFile(filepath.Join(metaDir, "plugin.json"), data, 0o644)
			},
			expectError: true,
			desc:        "invalid semver version should fail validation",
		},
		{
			name: "valid_manifest",
			manifestFn: func(dir string) error {
				metaDir := filepath.Join(dir, ".san-plugin")
				os.MkdirAll(metaDir, 0o755)
				m := Manifest{Name: "valid-plugin", Version: "1.2.3"}
				data, _ := json.Marshal(m)
				return os.WriteFile(filepath.Join(metaDir, "plugin.json"), data, 0o644)
			},
			expectError: false,
			desc:        "valid manifest should pass validation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			if err := tt.manifestFn(tmpDir); err != nil {
				t.Fatalf("setup error: %v", err)
			}

			err := ValidatePlugin(tmpDir)
			if tt.expectError && err == nil {
				t.Errorf("%s: expected error, got nil", tt.desc)
			} else if !tt.expectError && err != nil {
				t.Errorf("%s: unexpected error: %v", tt.desc, err)
			}
		})
	}
}

func TestLoadFromPath(t *testing.T) {
	// Create a test plugin
	tmpDir := t.TempDir()

	pluginMetaDir := filepath.Join(tmpDir, ".san-plugin")
	os.MkdirAll(pluginMetaDir, 0o755)

	manifest := Manifest{Name: "path-test"}
	manifestJSON, _ := json.Marshal(manifest)
	os.WriteFile(filepath.Join(pluginMetaDir, "plugin.json"), manifestJSON, 0o644)

	// Test LoadFromPath
	registry := NewRegistry()
	ctx := context.Background()

	if err := registry.LoadFromPath(ctx, tmpDir); err != nil {
		t.Fatalf("LoadFromPath() error = %v", err)
	}

	// Verify plugin was loaded
	if registry.Count() != 1 {
		t.Errorf("After LoadFromPath, Count() = %d, want 1", registry.Count())
	}

	// Plugins loaded via path should be enabled
	got, _ := registry.Get("path-test")
	if !got.Enabled {
		t.Error("Plugin loaded via path should be enabled")
	}
}

func TestHooksConfigParsing(t *testing.T) {
	tmpDir := t.TempDir()

	hooksDir := filepath.Join(tmpDir, "hooks")
	os.MkdirAll(hooksDir, 0o755)

	hooksJSON := `{
		"hooks": {
			"PostToolUse": [
				{
					"matcher": "Write|Edit",
					"hooks": [
						{
							"type": "command",
							"command": "${SAN_PLUGIN_ROOT}/scripts/format.sh",
							"async": true
						}
					]
				}
			]
		}
	}`
	os.WriteFile(filepath.Join(hooksDir, "hooks.json"), []byte(hooksJSON), 0o644)

	// Resolve hooks config
	config := ResolveHooksConfig(nil, tmpDir)
	if config == nil {
		t.Fatal("ResolveHooksConfig() returned nil")
	}

	postToolUse, ok := config.Hooks["PostToolUse"]
	if !ok {
		t.Fatal("Missing PostToolUse hooks")
	}
	if len(postToolUse) != 1 {
		t.Errorf("PostToolUse hooks length = %d, want 1", len(postToolUse))
	}

	matcher := postToolUse[0]
	if matcher.Matcher != "Write|Edit" {
		t.Errorf("Matcher = %q, want %q", matcher.Matcher, "Write|Edit")
	}
	if len(matcher.Hooks) != 1 {
		t.Errorf("Matcher hooks length = %d, want 1", len(matcher.Hooks))
	}

	cmd := matcher.Hooks[0]
	expectedCmd := tmpDir + "/scripts/format.sh"
	if cmd.Command != expectedCmd {
		t.Errorf("Hook command = %q, want %q", cmd.Command, expectedCmd)
	}
	if !cmd.Async {
		t.Error("Hook async should be true")
	}
}

func TestMCPConfigParsing(t *testing.T) {
	tmpDir := t.TempDir()

	mcpJSON := `{
		"mcpServers": {
			"database": {
				"command": "${SAN_PLUGIN_ROOT}/servers/db",
				"args": ["--config", "${SAN_PLUGIN_ROOT}/config.json"],
				"env": {
					"DB_PATH": "${SAN_PLUGIN_ROOT}/data"
				}
			}
		}
	}`
	os.WriteFile(filepath.Join(tmpDir, ".mcp.json"), []byte(mcpJSON), 0o644)

	// Resolve MCP config
	servers := ResolveMCPServers(nil, tmpDir)
	if servers == nil {
		t.Fatal("ResolveMCPServers() returned nil")
	}

	db, ok := servers["database"]
	if !ok {
		t.Fatal("Missing database server")
	}

	expectedCmd := tmpDir + "/servers/db"
	if db.Command != expectedCmd {
		t.Errorf("Server command = %q, want %q", db.Command, expectedCmd)
	}

	if len(db.Args) != 2 {
		t.Fatalf("Server args length = %d, want 2", len(db.Args))
	}
	expectedArg := tmpDir + "/config.json"
	if db.Args[1] != expectedArg {
		t.Errorf("Server arg[1] = %q, want %q", db.Args[1], expectedArg)
	}

	expectedEnv := tmpDir + "/data"
	if db.Env["DB_PATH"] != expectedEnv {
		t.Errorf("Server env DB_PATH = %q, want %q", db.Env["DB_PATH"], expectedEnv)
	}
}
