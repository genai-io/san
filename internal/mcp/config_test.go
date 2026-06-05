package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigLoader_SaveAndLoad(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "mcp-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create project dir
	projectDir := filepath.Join(tmpDir, ".san")
	os.MkdirAll(projectDir, 0o755)

	loader := &ConfigLoader{
		userDir:    filepath.Join(tmpDir, "user", ".san"),
		projectDir: projectDir,
	}

	// Test saving a server config
	config := ServerConfig{
		Type:    TransportSTDIO,
		Command: "echo",
		Args:    []string{"hello"},
		Env:     map[string]string{"FOO": "bar"},
	}

	err = loader.SaveServer("test-server", config, ScopeLocal)
	if err != nil {
		t.Fatalf("Failed to save server: %v", err)
	}

	// Verify file exists
	localPath := filepath.Join(projectDir, "mcp.local.json")
	if _, err := os.Stat(localPath); os.IsNotExist(err) {
		t.Fatal("Config file was not created")
	}

	// Load and verify
	configs, err := loader.LoadAll()
	if err != nil {
		t.Fatalf("Failed to load configs: %v", err)
	}

	if len(configs) != 1 {
		t.Fatalf("Expected 1 config, got %d", len(configs))
	}

	loaded, ok := configs["test-server"]
	if !ok {
		t.Fatal("Server 'test-server' not found in loaded configs")
	}

	if loaded.Command != "echo" {
		t.Errorf("Expected command 'echo', got '%s'", loaded.Command)
	}

	if len(loaded.Args) != 1 || loaded.Args[0] != "hello" {
		t.Errorf("Args mismatch: %v", loaded.Args)
	}
}

func TestConfigLoader_ScopePriority(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "mcp-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	userDir := filepath.Join(tmpDir, "user", ".san")
	projectDir := filepath.Join(tmpDir, "project", ".san")
	os.MkdirAll(userDir, 0o755)
	os.MkdirAll(projectDir, 0o755)

	// Create user-level config
	userConfig := MCPConfig{
		MCPServers: map[string]ServerConfig{
			"shared": {
				Command: "user-command",
			},
		},
	}
	userData, _ := json.Marshal(userConfig)
	os.WriteFile(filepath.Join(userDir, "mcp.json"), userData, 0o644)

	// Create project-level config (should override)
	projectConfig := MCPConfig{
		MCPServers: map[string]ServerConfig{
			"shared": {
				Command: "project-command",
			},
		},
	}
	projectData, _ := json.Marshal(projectConfig)
	os.WriteFile(filepath.Join(projectDir, "mcp.json"), projectData, 0o644)

	loader := &ConfigLoader{
		userDir:    userDir,
		projectDir: projectDir,
	}

	configs, err := loader.LoadAll()
	if err != nil {
		t.Fatalf("Failed to load configs: %v", err)
	}

	// Project config should override user config
	if configs["shared"].Command != "project-command" {
		t.Errorf("Expected project-command, got %s", configs["shared"].Command)
	}
}

func TestServerConfig_GetType(t *testing.T) {
	tests := []struct {
		name     string
		config   ServerConfig
		expected TransportType
	}{
		{
			name:     "default to stdio",
			config:   ServerConfig{Command: "echo"},
			expected: TransportSTDIO,
		},
		{
			name:     "infer http from URL",
			config:   ServerConfig{URL: "https://example.com"},
			expected: TransportHTTP,
		},
		{
			name:     "explicit http",
			config:   ServerConfig{Type: TransportHTTP, URL: "https://example.com"},
			expected: TransportHTTP,
		},
		{
			name:     "explicit sse",
			config:   ServerConfig{Type: TransportSSE, URL: "https://example.com"},
			expected: TransportSSE,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.config.GetType()
			if got != tt.expected {
				t.Errorf("GetType() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func Test_parseMCPToolName(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantServer string
		wantTool   string
		wantOk     bool
	}{
		{
			name:       "valid mcp tool",
			input:      "mcp__filesystem__read_file",
			wantServer: "filesystem",
			wantTool:   "read_file",
			wantOk:     true,
		},
		{
			name:       "valid with dashes",
			input:      "mcp__my-server__my-tool",
			wantServer: "my-server",
			wantTool:   "my-tool",
			wantOk:     true,
		},
		{
			name:   "not mcp tool",
			input:  "Read",
			wantOk: false,
		},
		{
			name:   "incomplete prefix",
			input:  "mcp_server__tool",
			wantOk: false,
		},
		{
			name:   "missing tool",
			input:  "mcp__server",
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, tool, ok := parseMCPToolName(tt.input)
			if ok != tt.wantOk {
				t.Errorf("parseMCPToolName() ok = %v, want %v", ok, tt.wantOk)
			}
			if ok {
				if server != tt.wantServer {
					t.Errorf("parseMCPToolName() server = %v, want %v", server, tt.wantServer)
				}
				if tool != tt.wantTool {
					t.Errorf("parseMCPToolName() tool = %v, want %v", tool, tt.wantTool)
				}
			}
		})
	}
}

func TestMCP_ResourceListing(t *testing.T) {
	// Create a client; before connect, cached resources should be empty
	client := NewClient(ServerConfig{Command: "echo"})

	resources := client.GetCachedResources()
	if len(resources) != 0 {
		t.Errorf("expected empty cached resources before connect, got %d", len(resources))
	}

	// Inject resources directly (same package) to simulate a connected state
	client.mu.Lock()
	client.connected = true
	client.resources = []MCPResource{
		{URI: "file:///tmp/test.txt", Name: "test.txt", MimeType: "text/plain"},
		{URI: "file:///tmp/data.json", Name: "data.json", MimeType: "application/json"},
	}
	client.mu.Unlock()

	cached := client.GetCachedResources()
	if len(cached) != 2 {
		t.Fatalf("expected 2 cached resources, got %d", len(cached))
	}
	if cached[0].URI != "file:///tmp/test.txt" {
		t.Errorf("expected URI 'file:///tmp/test.txt', got %q", cached[0].URI)
	}
	if cached[1].MimeType != "application/json" {
		t.Errorf("expected MimeType 'application/json', got %q", cached[1].MimeType)
	}
	if cached[0].Name != "test.txt" {
		t.Errorf("expected Name 'test.txt', got %q", cached[0].Name)
	}
}

func TestConfigLoader_SaveServer_StripsMetadataFieldsFromDisk(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewConfigLoaderForTest(tmpDir)

	err := loader.SaveServer("test-srv", ServerConfig{
		Name:    "should-not-persist",
		Scope:   ScopeProject,
		Type:    TransportHTTP,
		URL:     "https://example.com/mcp",
		Headers: map[string]string{"Authorization": "Bearer token"},
	}, ScopeProject)
	if err != nil {
		t.Fatalf("SaveServer() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(loader.projectDir, "mcp.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	servers := raw["mcpServers"].(map[string]any)
	saved := servers["test-srv"].(map[string]any)
	if _, ok := saved["name"]; ok {
		t.Fatal("server metadata field 'name' should not be persisted")
	}
	if _, ok := saved["scope"]; ok {
		t.Fatal("server metadata field 'scope' should not be persisted")
	}
	if saved["url"] != "https://example.com/mcp" {
		t.Fatalf("expected URL to persist, got %v", saved["url"])
	}
}

func TestConfigLoader_RemoveServerFromAll_RemovesEveryScope(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewConfigLoaderForTest(tmpDir)

	for _, scope := range []Scope{ScopeUser, ScopeProject, ScopeLocal} {
		err := loader.SaveServer("shared", ServerConfig{
			Type:    TransportSTDIO,
			Command: "echo",
		}, scope)
		if err != nil {
			t.Fatalf("SaveServer(%s) error = %v", scope, err)
		}
	}

	if err := loader.RemoveServerFromAll("shared"); err != nil {
		t.Fatalf("RemoveServerFromAll() error = %v", err)
	}

	for _, file := range []string{
		filepath.Join(loader.userDir, "mcp.json"),
		filepath.Join(loader.projectDir, "mcp.json"),
		filepath.Join(loader.projectDir, "mcp.local.json"),
	} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", file, err)
		}
		var cfg MCPConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			t.Fatalf("Unmarshal(%s) error = %v", file, err)
		}
		if _, ok := cfg.MCPServers["shared"]; ok {
			t.Fatalf("expected shared to be removed from %s", file)
		}
	}
}

func TestNewRegistry_IncludesPluginServers(t *testing.T) {
	reg, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	reg.PluginServers = func() []PluginServer {
		return []PluginServer{
			{Name: "demo:db", Command: "echo"},
		}
	}
	reg.configs = reg.mergePluginMCPConfigs(reg.configs)

	cfg, ok := reg.GetConfig("demo:db")
	if !ok {
		t.Fatal("expected plugin MCP server to be present in registry")
	}
	if cfg.Command != "echo" {
		t.Fatalf("unexpected plugin MCP command: %q", cfg.Command)
	}
}
