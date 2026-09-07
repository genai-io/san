package mcp_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/mcp"
)

// ---------------------------------------------------------------------------
// Registry tests
// ---------------------------------------------------------------------------

// newFakeRegistry creates a registry with N server configs for testing.
// The configs are not connected; use this for testing config listing and
// disconnected state scenarios.
func newFakeRegistry(names ...string) *mcp.Registry {
	configs := make(map[string]mcp.ServerConfig, len(names))
	for _, name := range names {
		configs[name] = mcp.ServerConfig{Name: name, Command: "fake-" + name}
	}
	return mcp.NewRegistryForTest(configs)
}

func TestRegistry_ListConfigs(t *testing.T) {
	registry := newFakeRegistry("server-a", "server-b")

	servers := registry.List()
	if len(servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(servers))
	}

	// All should be disconnected
	for _, s := range servers {
		if s.Status != mcp.StatusDisconnected {
			t.Errorf("server %q: expected disconnected, got %q", s.Config.Name, s.Status)
		}
	}
}

func TestRegistry_GetConfig(t *testing.T) {
	registry := newFakeRegistry("my-server")

	cfg, ok := registry.GetConfig("my-server")
	if !ok {
		t.Fatal("expected to find 'my-server' config")
	}
	if cfg.Name != "my-server" {
		t.Errorf("expected name 'my-server', got %q", cfg.Name)
	}

	_, ok = registry.GetConfig("nonexistent")
	if ok {
		t.Error("expected not to find 'nonexistent'")
	}
}

func TestRegistry_GetClient_NotConnected(t *testing.T) {
	registry := newFakeRegistry("srv")

	_, ok := registry.GetClient("srv")
	if ok {
		t.Error("expected no client before Connect()")
	}
}

func TestRegistry_GetToolSchemas_Empty(t *testing.T) {
	registry := newFakeRegistry("srv")

	tools := registry.GetToolSchemas()
	if len(tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(tools))
	}
}

func TestRegistry_CallTool_InvalidName(t *testing.T) {
	registry := newFakeRegistry("srv")

	_, err := registry.CallTool(context.Background(), "not-mcp-tool", nil)
	if err == nil {
		t.Error("expected error for invalid MCP tool name")
	}
}

func TestRegistry_CallTool_NotConnected(t *testing.T) {
	registry := newFakeRegistry("srv")

	_, err := registry.CallTool(context.Background(), "mcp__srv__read_file", nil)
	if err == nil {
		t.Error("expected error for unconnected server")
	}
}

func TestRegistry_DisconnectAll_Empty(t *testing.T) {
	registry := newFakeRegistry("a", "b")
	// Should not panic on empty clients
	registry.DisconnectAll()
}

func TestRegistry_OnToolsChanged(t *testing.T) {
	registry := newFakeRegistry("srv")

	var called bool
	registry.SetOnToolsChanged(func() { called = true })

	// Trigger via Disconnect (internally notifies)
	registry.Disconnect("srv")

	// The callback is called on connect/disconnect
	// Since no client was connected, Disconnect is a no-op and doesn't trigger
	if called {
		t.Error("callback should not fire when no client was disconnected")
	}
}

// ---------------------------------------------------------------------------
// IsMCPTool (registry utility)
// ---------------------------------------------------------------------------

func TestIsMCPTool(t *testing.T) {
	if !mcp.IsMCPTool("mcp__srv__tool") {
		t.Error("expected true for valid MCP tool name")
	}
	if mcp.IsMCPTool("Read") {
		t.Error("expected false for non-MCP tool name")
	}
}

// ---------------------------------------------------------------------------
// Config: ServerConfig.GetType
// ---------------------------------------------------------------------------

func TestServerConfig_GetType(t *testing.T) {
	tests := []struct {
		name   string
		config mcp.ServerConfig
		want   mcp.TransportType
	}{
		{"default stdio", mcp.ServerConfig{Command: "echo"}, mcp.TransportSTDIO},
		{"infer http from URL", mcp.ServerConfig{URL: "https://example.com"}, mcp.TransportHTTP},
		{"explicit sse", mcp.ServerConfig{Type: mcp.TransportSSE, URL: "https://example.com/sse"}, mcp.TransportSSE},
		{"explicit http", mcp.ServerConfig{Type: mcp.TransportHTTP, URL: "https://example.com"}, mcp.TransportHTTP},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.config.GetType()
			if got != tt.want {
				t.Errorf("GetType() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Config: SaveServer + LoadAll round-trip
// ---------------------------------------------------------------------------

func TestConfigLoader_RoundTrip(t *testing.T) {
	loader := mcp.NewConfigLoaderForTest(t.TempDir())

	config := mcp.ServerConfig{
		Type:    mcp.TransportSTDIO,
		Command: "node",
		Args:    []string{"server.js"},
		Env:     map[string]string{"DEBUG": "true"},
	}

	if err := loader.SaveServer("test-srv", config, mcp.ScopeLocal); err != nil {
		t.Fatalf("SaveServer() error: %v", err)
	}

	configs, err := loader.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll() error: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("expected 1 config, got %d", len(configs))
	}

	loaded := configs["test-srv"]
	if loaded.Command != "node" {
		t.Errorf("expected command 'node', got %q", loaded.Command)
	}
	if len(loaded.Args) != 1 || loaded.Args[0] != "server.js" {
		t.Errorf("unexpected args: %v", loaded.Args)
	}
	if loaded.Scope != mcp.ScopeLocal {
		t.Errorf("expected ScopeLocal, got %q", loaded.Scope)
	}
}

func TestConfigLoader_ScopePriority(t *testing.T) {
	loader := mcp.NewConfigLoaderForTest(t.TempDir())

	// Save user-scope config
	if err := loader.SaveServer("shared", mcp.ServerConfig{Command: "user-cmd"}, mcp.ScopeUser); err != nil {
		t.Fatalf("SaveServer(user) error: %v", err)
	}

	// Save project-scope config (should override)
	if err := loader.SaveServer("shared", mcp.ServerConfig{Command: "project-cmd"}, mcp.ScopeProject); err != nil {
		t.Fatalf("SaveServer(project) error: %v", err)
	}

	configs, err := loader.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll() error: %v", err)
	}

	if configs["shared"].Command != "project-cmd" {
		t.Errorf("expected project-cmd to override, got %q", configs["shared"].Command)
	}
}

func TestConfigLoader_RemoveServer(t *testing.T) {
	loader := mcp.NewConfigLoaderForTest(t.TempDir())

	if err := loader.SaveServer("to-remove", mcp.ServerConfig{Command: "echo"}, mcp.ScopeLocal); err != nil {
		t.Fatalf("SaveServer() error: %v", err)
	}

	if err := loader.RemoveServer("to-remove", mcp.ScopeLocal); err != nil {
		t.Fatalf("RemoveServer() error: %v", err)
	}

	configs, err := loader.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll() error: %v", err)
	}
	if _, ok := configs["to-remove"]; ok {
		t.Error("expected server to be removed")
	}
}

// ---------------------------------------------------------------------------
// Real MCP server integration tests (require npx / node)
// ---------------------------------------------------------------------------

// requireNpx skips the test if npx is not available.
func requireNpx(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping real MCP server test in short mode")
	}
	if os.Getenv("SAN_RUN_REAL_MCP") != "1" {
		t.Skip("set SAN_RUN_REAL_MCP=1 to run real MCP server tests")
	}
	// Check npx is available
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx not found, skipping real MCP server test")
	}
}

// TestRealMCP_Everything connects to @modelcontextprotocol/server-everything,
// the official MCP reference/test server that exercises all protocol features:
// tools, resources, and prompts — no authentication required.
//
// See: https://www.npmjs.com/package/@modelcontextprotocol/server-everything
func TestRealMCP_Everything(t *testing.T) {
	requireNpx(t)

	client := mcp.NewClient(mcp.ServerConfig{
		Name:    "everything",
		Type:    mcp.TransportSTDIO,
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-everything"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	defer client.Disconnect()

	if !client.IsConnected() {
		t.Fatal("expected connected")
	}

	// --- Tools ---
	// server-everything sends tools/list_changed notification asynchronously,
	// so tools may not be cached immediately. Wait briefly for them to appear.
	var tools []mcp.MCPTool
	for range 20 {
		tools = client.GetCachedTools()
		if len(tools) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Logf("tools: %d", len(tools))
	if len(tools) == 0 {
		t.Fatal("expected at least 1 tool from server-everything")
	}

	// Find the "echo" tool
	var hasEcho bool
	for _, tool := range tools {
		if tool.Name == "echo" {
			hasEcho = true
			if tool.Description == "" {
				t.Error("echo tool should have a description")
			}
			if len(tool.InputSchema) == 0 {
				t.Error("echo tool should have an input schema")
			}
			break
		}
	}
	if !hasEcho {
		t.Error("expected 'echo' tool in server-everything")
	}

	// --- Call echo tool ---
	result, err := client.CallTool(ctx, "echo", map[string]any{"message": "hello from san"})
	if err != nil {
		t.Fatalf("CallTool(echo) error: %v", err)
	}
	if result.IsError {
		t.Errorf("echo tool returned error: %v", result.Content)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected non-empty result from echo")
	}
	if !strings.Contains(result.Content[0].Text, "hello from san") {
		t.Errorf("echo should return our message, got: %q", result.Content[0].Text)
	}

	// --- Call add tool ---
	addResult, err := client.CallTool(ctx, "get-sum", map[string]any{"a": 3, "b": 7})
	if err != nil {
		t.Fatalf("CallTool(get-sum) error: %v", err)
	}
	if addResult.IsError {
		t.Errorf("get-sum returned error: %v", addResult.Content)
	}
	if len(addResult.Content) > 0 {
		t.Logf("get-sum result: %s", addResult.Content[0].Text)
		if !strings.Contains(addResult.Content[0].Text, "10") {
			t.Errorf("expected sum=10, got: %q", addResult.Content[0].Text)
		}
	}

	// --- Resources ---
	resources := client.ToServer().Resources
	t.Logf("resources: %d", len(resources))
	if len(resources) == 0 {
		t.Error("expected at least 1 resource from server-everything")
	}

	// --- Prompts ---
	prompts := client.ToServer().Prompts
	t.Logf("prompts: %d", len(prompts))
	if len(prompts) == 0 {
		t.Error("expected at least 1 prompt from server-everything")
	}

}

// TestRealMCP_Filesystem connects to @modelcontextprotocol/server-filesystem,
// the official MCP filesystem server. Tests file read operations on a temp dir.
//
// See: https://www.npmjs.com/package/@modelcontextprotocol/server-filesystem
func TestRealMCP_Filesystem(t *testing.T) {
	requireNpx(t)

	// Create a temp dir with a test file.
	// Resolve symlinks because on macOS /var → /private/var and the
	// filesystem server uses the real path for access control.
	tmpDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks() error: %v", err)
	}
	testFile := filepath.Join(tmpDir, "hello.txt")
	if err := os.WriteFile(testFile, []byte("hello from integration test"), 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	client := mcp.NewClient(mcp.ServerConfig{
		Name:    "filesystem",
		Type:    mcp.TransportSTDIO,
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-filesystem", tmpDir},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	defer client.Disconnect()

	if !client.IsConnected() {
		t.Fatal("expected connected")
	}

	// --- Tools ---
	tools := client.GetCachedTools()
	t.Logf("tools: %d", len(tools))
	if len(tools) == 0 {
		t.Fatal("expected at least 1 tool from server-filesystem")
	}

	// Log available tools
	toolNames := make([]string, len(tools))
	for i, tool := range tools {
		toolNames[i] = tool.Name
	}
	t.Logf("available tools: %v", toolNames)

	// --- Read the test file ---
	result, err := client.CallTool(ctx, "read_file", map[string]any{"path": testFile})
	if err != nil {
		t.Fatalf("CallTool(read_file) error: %v", err)
	}
	if result.IsError {
		t.Fatalf("read_file returned error: %v", result.Content)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected non-empty content from read_file")
	}
	if !strings.Contains(result.Content[0].Text, "hello from integration test") {
		t.Errorf("expected file content, got: %q", result.Content[0].Text)
	}

	// --- List directory ---
	dirResult, err := client.CallTool(ctx, "list_directory", map[string]any{"path": tmpDir})
	if err != nil {
		t.Fatalf("CallTool(list_directory) error: %v", err)
	}
	if dirResult.IsError {
		t.Fatalf("list_directory returned error: %v", dirResult.Content)
	}
	if len(dirResult.Content) == 0 {
		t.Fatal("expected directory listing content")
	}
	if !strings.Contains(dirResult.Content[0].Text, "hello.txt") {
		t.Errorf("expected 'hello.txt' in listing, got: %q", dirResult.Content[0].Text)
	}

	// --- Write + read round-trip ---
	writeFile := filepath.Join(tmpDir, "written.txt")
	writeResult, err := client.CallTool(ctx, "write_file", map[string]any{
		"path":    writeFile,
		"content": "written by MCP test",
	})
	if err != nil {
		t.Fatalf("CallTool(write_file) error: %v", err)
	}
	if writeResult.IsError {
		t.Fatalf("write_file returned error: %v", writeResult.Content)
	}

	// Verify the file was actually written to disk
	data, err := os.ReadFile(writeFile)
	if err != nil {
		t.Fatalf("failed to read written file: %v", err)
	}
	if string(data) != "written by MCP test" {
		t.Errorf("file content mismatch: %q", string(data))
	}

}

// TestRealMCP_Registry_EndToEnd uses server-everything through the Registry,
// testing the full flow: connect → get tool schemas → call tool → disconnect.
func TestRealMCP_Registry_EndToEnd(t *testing.T) {
	requireNpx(t)

	configs := map[string]mcp.ServerConfig{
		"everything": {
			Name:    "everything",
			Type:    mcp.TransportSTDIO,
			Command: "npx",
			Args:    []string{"-y", "@modelcontextprotocol/server-everything"},
		},
	}
	registry := mcp.NewRegistryForTest(configs)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Connect
	if err := registry.Connect(ctx, "everything"); err != nil {
		t.Fatalf("Registry.Connect() error: %v", err)
	}
	defer registry.DisconnectAll()

	// Tool schemas — wait for async tool list to populate
	var schemas []core.ToolSchema
	for range 20 {
		schemas = registry.GetToolSchemas()
		if len(schemas) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Logf("tool schemas: %d", len(schemas))
	if len(schemas) == 0 {
		t.Fatal("expected tool schemas from everything server")
	}

	// Verify tool naming convention: mcp__everything__<tool>
	for _, s := range schemas {
		if !strings.HasPrefix(s.Name, "mcp__everything__") {
			t.Errorf("unexpected tool name format: %q", s.Name)
		}
	}

	// Call echo via registry
	result, err := registry.CallTool(ctx, "mcp__everything__echo", map[string]any{"message": "registry test"})
	if err != nil {
		t.Fatalf("Registry.CallTool() error: %v", err)
	}
	if result.IsError {
		t.Errorf("echo returned error: %v", result.Content)
	}
	if len(result.Content) > 0 && !strings.Contains(result.Content[0].Text, "registry test") {
		t.Errorf("expected echoed message, got: %q", result.Content[0].Text)
	}

	// Disconnect. The registry drops the server synchronously; the transport
	// teardown is detached, so nothing to wait on here.
	registry.Disconnect("everything")

	// After disconnect, tool schemas should be empty
	schemas = registry.GetToolSchemas()
	if len(schemas) != 0 {
		t.Errorf("expected 0 tool schemas after disconnect, got %d", len(schemas))
	}
}
