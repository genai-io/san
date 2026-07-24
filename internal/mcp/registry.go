package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"github.com/genai-io/san/internal/atomicfile"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/log"
	"go.uber.org/zap"
)

// PluginServer describes an MCP server contributed by a plugin.
// Fields use primitive types so callers don't need mcp-internal types.
type PluginServer struct {
	Name    string
	Type    string
	Command string
	Args    []string
	Env     map[string]string
	URL     string
	Headers map[string]string
	Scope   string
}

// Registry manages multiple MCP server connections
type Registry struct {
	mu           sync.RWMutex
	connectionMu sync.Mutex
	clients      map[string]*Client
	configs      map[string]ServerConfig
	disabled     map[string]bool           // servers explicitly disabled by the user
	connecting   map[string]bool           // servers currently being connected (async)
	connectErr   map[string]string         // last connection error for servers without a client
	leasing      map[string]map[uint64]int // active per-agent users grouped by connection generation
	temporary    map[string]bool           // connections created solely for active leases
	persistent   map[string]bool           // explicit connection intent, retained across reconnects
	generation   map[string]uint64         // changes whenever a connection is replaced
	loader       *ConfigLoader
	cwd          string

	// PluginServers returns MCP servers contributed by plugins. Injected by
	// the app layer to avoid mcp importing plugin (same-layer dependency).
	PluginServers func() []PluginServer

	clientFactory func(ServerConfig) *Client // test seam; nil uses NewClient

	// Callback when tool schemas change
	onToolsChanged func()
}

// mcpState is the on-disk format for persisted MCP runtime state.
type mcpState struct {
	Disabled []string `json:"disabled,omitempty"`
}

// defaultRegistry is the package-level registry; registryMu guards the pointer
// itself. The Registry it points at locks its own contents — what needed
// guarding is the swap, which Initialize performs on the bubbletea goroutine
// (reloadProjectServices) while DefaultRegistry() is read elsewhere.
var (
	registryMu      sync.RWMutex
	registryAdoptMu sync.Mutex
	defaultRegistry = newEmptyRegistry()
)

// adoptLiveClients moves connections from the outgoing registry into this one
// for every server whose configuration is unchanged, and tears down the rest.
//
// Without it a cwd change replaced the registry with a fresh, zero-client one:
// GetToolSchemas returned nothing, so every mcp__* tool silently vanished from
// the agent for the rest of the session, and the outgoing registry's stdio
// subprocesses were dropped on the floor still running.
//
// The teardown of servers that did NOT survive runs detached: Client.Disconnect
// waits on the child's read loop and exit, up to seven seconds per server, and
// Initialize is called from the bubbletea goroutine.
func (r *Registry) adoptLiveClients(old *Registry) {
	if old == nil || old == r {
		return
	}

	registryAdoptMu.Lock()
	defer registryAdoptMu.Unlock()
	old.connectionMu.Lock()
	defer old.connectionMu.Unlock()
	r.connectionMu.Lock()
	defer r.connectionMu.Unlock()

	old.mu.Lock()
	adopted := make(map[string]*Client)
	stale := make(map[string]*Client)
	for name, client := range old.clients {
		cfg, stillConfigured := r.configs[name]
		if !old.persistent[name] {
			continue
		}
		if stillConfigured && reflect.DeepEqual(cfg, old.configs[name]) && client.IsConnected() {
			adopted[name] = client
			continue
		}
		stale[name] = client
	}
	for name, client := range adopted {
		delete(old.clients, name)
		delete(old.persistent, name)
		client.SetOnToolsChanged(r.notifyToolsChanged)
	}
	for name := range stale {
		delete(old.clients, name)
		delete(old.persistent, name)
	}
	old.mu.Unlock()

	r.mu.Lock()
	maps.Copy(r.clients, adopted)
	for name := range adopted {
		r.persistent[name] = true
	}
	r.mu.Unlock()

	if len(stale) > 0 {
		go func() {
			for _, c := range stale {
				_ = c.Disconnect()
			}
		}()
	}
}

func newEmptyRegistry() *Registry {
	return &Registry{
		clients:    make(map[string]*Client),
		configs:    make(map[string]ServerConfig),
		disabled:   make(map[string]bool),
		connecting: make(map[string]bool),
		connectErr: make(map[string]string),
		leasing:    make(map[string]map[uint64]int),
		temporary:  make(map[string]bool),
		persistent: make(map[string]bool),
		generation: make(map[string]uint64),
	}
}

// NewRegistryForTest creates a registry with pre-loaded configs for testing.
// It does not read from disk.
func NewRegistryForTest(configs map[string]ServerConfig) *Registry {
	return &Registry{
		clients:    make(map[string]*Client),
		configs:    configs,
		disabled:   make(map[string]bool),
		connecting: make(map[string]bool),
		connectErr: make(map[string]string),
		leasing:    make(map[string]map[uint64]int),
		temporary:  make(map[string]bool),
		persistent: make(map[string]bool),
		generation: make(map[string]uint64),
	}
}

// NewRegistry creates a new MCP registry
func NewRegistry(cwd string) (*Registry, error) {
	loader := NewConfigLoader(cwd)
	configs, err := loader.LoadAll()
	if err != nil {
		return nil, fmt.Errorf("failed to load MCP configs: %w", err)
	}

	reg := &Registry{
		clients:    make(map[string]*Client),
		configs:    configs,
		disabled:   make(map[string]bool),
		connecting: make(map[string]bool),
		connectErr: make(map[string]string),
		leasing:    make(map[string]map[uint64]int),
		temporary:  make(map[string]bool),
		persistent: make(map[string]bool),
		generation: make(map[string]uint64),
		loader:     loader,
		cwd:        cwd,
	}
	reg.configs = reg.mergePluginMCPConfigs(configs)
	reg.loadState()
	return reg, nil
}

// Reload reloads configurations from disk.
// Clients whose server config no longer exists are disconnected to avoid
// orphaned connections.
func (r *Registry) Reload() error {
	configs, err := r.loader.LoadAll()
	if err != nil {
		return err
	}
	configs = r.mergePluginMCPConfigs(configs)

	r.connectionMu.Lock()
	var stale = make(map[string]*Client)
	r.mu.Lock()
	// Disconnect clients whose config was removed or changed. Active temporary
	// lease-owned clients stay with their current registry until their owner
	// releases them; reload must not invalidate an in-flight Agent run.
	for name, client := range r.clients {
		config, ok := configs[name]
		if !r.persistent[name] || ok && reflect.DeepEqual(config, r.configs[name]) {
			continue
		}
		stale[name] = client
		delete(r.clients, name)
		delete(r.connecting, name)
		delete(r.connectErr, name)
		delete(r.leasing, name)
		delete(r.temporary, name)
		delete(r.persistent, name)
		r.generation[name]++
	}
	r.configs = configs
	r.mu.Unlock()
	r.connectionMu.Unlock()

	for name, client := range stale {
		closeInBackground(name, client)
	}
	if len(stale) > 0 {
		r.notifyToolsChanged()
	}
	return nil
}

func (r *Registry) mergePluginMCPConfigs(configs map[string]ServerConfig) map[string]ServerConfig {
	merged := make(map[string]ServerConfig, len(configs))
	maps.Copy(merged, configs)
	if r.PluginServers == nil {
		return merged
	}
	for _, srv := range r.PluginServers() {
		merged[srv.Name] = ServerConfig{
			Name:    srv.Name,
			Type:    TransportType(srv.Type),
			Command: srv.Command,
			Args:    append([]string(nil), srv.Args...),
			Env:     maps.Clone(srv.Env),
			URL:     srv.URL,
			Headers: maps.Clone(srv.Headers),
			Scope:   Scope(srv.Scope),
		}
	}
	return merged
}

// AddServer adds a new server configuration
func (r *Registry) AddServer(name string, config ServerConfig, scope Scope) error {
	if err := r.loader.SaveServer(name, config, scope); err != nil {
		return err
	}

	r.mu.Lock()
	config.Name = name
	config.Scope = scope
	r.configs[name] = config
	r.mu.Unlock()

	return nil
}

// RemoveServer removes a server configuration
func (r *Registry) RemoveServer(name string) error {
	r.connectionMu.Lock()
	defer r.connectionMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()

	// Disconnect if connected (best-effort; errors are ignored during cleanup)
	if client, ok := r.clients[name]; ok {
		_ = client.Disconnect()
		delete(r.clients, name)
		r.generation[name]++
	}

	// Remove from all configs
	if err := r.loader.RemoveServerFromAll(name); err != nil {
		return err
	}

	delete(r.configs, name)
	delete(r.connecting, name)
	delete(r.connectErr, name)
	delete(r.leasing, name)
	delete(r.temporary, name)
	delete(r.persistent, name)
	return nil
}

// Connect connects to an MCP server and keeps the connection persistent until
// an explicit disconnect, even if a custom Agent originally created it.
func (r *Registry) Connect(ctx context.Context, name string) error {
	r.connectionMu.Lock()
	connected, err := r.connectLocked(ctx, name)
	if err == nil {
		r.mu.Lock()
		r.persistent[name] = true
		delete(r.temporary, name)
		r.mu.Unlock()
	}
	r.connectionMu.Unlock()

	if connected {
		r.notifyToolsChanged()
	}
	return err
}

// connectLocked establishes one connection and reports whether it installed
// the registry's current client. The caller must hold connectionMu so duplicate
// transports cannot be created and ownership changes stay atomic.
func (r *Registry) connectLocked(ctx context.Context, name string) (bool, error) {
	r.mu.Lock()
	config, ok := r.configs[name]
	if !ok {
		r.mu.Unlock()
		return false, fmt.Errorf("server not found: %s", name)
	}
	if client, ok := r.clients[name]; ok && client.IsConnected() {
		r.mu.Unlock()
		return false, nil
	}
	factory := r.clientFactory
	r.mu.Unlock()

	client := NewClient(config)
	if factory != nil {
		client = factory(config)
	}
	if err := client.Connect(ctx); err != nil {
		return false, fmt.Errorf("failed to connect to %s: %w", name, err)
	}
	client.SetOnToolsChanged(r.notifyToolsChanged)

	r.mu.Lock()
	r.clients[name] = client
	if r.generation == nil {
		r.generation = make(map[string]uint64)
	}
	r.generation[name]++
	r.mu.Unlock()
	return true, nil
}

// acquireServer keeps a configured server connected for one custom-Agent run.
// The first lease owns a newly-created temporary connection; later leases share
// it. A connection that predated all leases remains persistent after release.
func (r *Registry) acquireServer(ctx context.Context, name string) (uint64, error) {
	r.connectionMu.Lock()
	connected, err := r.connectLocked(ctx, name)
	if err != nil {
		r.connectionMu.Unlock()
		return 0, err
	}

	r.mu.Lock()
	if r.leasing == nil {
		r.leasing = make(map[string]map[uint64]int)
	}
	generation := r.generation[name]
	if r.leasing[name] == nil {
		r.leasing[name] = make(map[uint64]int)
	}
	r.leasing[name][generation]++
	if connected && !r.persistent[name] {
		r.temporary[name] = true
	}
	r.mu.Unlock()
	r.connectionMu.Unlock()

	if connected {
		r.notifyToolsChanged()
	}
	return generation, nil
}

// releaseServer drops one custom-Agent lease and disconnects only a temporary
// connection whose final lease ended. A stale cleanup from an older connection
// generation cannot affect a replacement connection with the same name.
func (r *Registry) releaseServer(name string, generation uint64) {
	r.connectionMu.Lock()

	var client *Client

	r.mu.Lock()
	generationLeases := r.leasing[name]
	if generationLeases == nil || generationLeases[generation] == 0 {
		r.mu.Unlock()
		r.connectionMu.Unlock()
		return
	}
	if generationLeases[generation] > 1 {
		generationLeases[generation]--
		r.mu.Unlock()
		r.connectionMu.Unlock()
		return
	}
	delete(generationLeases, generation)
	if len(generationLeases) == 0 {
		delete(r.leasing, name)
	}
	if r.generation[name] == generation && r.temporary[name] && !r.persistent[name] {
		client = r.clients[name]
		delete(r.clients, name)
		delete(r.temporary, name)
	}
	r.mu.Unlock()
	r.connectionMu.Unlock()

	if client != nil {
		closeInBackground(name, client)
		r.notifyToolsChanged()
	}
}

// ConnectAll connects to all configured MCP servers.
// Connection errors are collected but don't stop other connections.
func (r *Registry) ConnectAll(ctx context.Context) []error {
	r.mu.RLock()
	names := make([]string, 0, len(r.configs))
	for name := range r.configs {
		names = append(names, name)
	}
	r.mu.RUnlock()

	var errs []error
	for _, name := range names {
		if err := r.Connect(ctx, name); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	return errs
}

// Disconnect disconnects from an MCP server
// Disconnect drops a server from the registry. The server is gone from the
// registry's point of view when this returns; the transport teardown runs in
// the background.
//
// Detached because Client.Disconnect waits on the child's read loop and then
// its exit — up to seven seconds for a wedged server — and this is called from
// the bubbletea Update goroutine (the /mcp panel, /mcp disconnect). Doing it
// inline froze the UI for that whole time, and doing it under r.mu froze the
// agent goroutine with it, since CallTool takes the read lock.
func (r *Registry) Disconnect(name string) {
	r.connectionMu.Lock()

	r.mu.Lock()
	client, ok := r.clients[name]
	if ok {
		delete(r.clients, name)
		r.generation[name]++
	}
	delete(r.leasing, name)
	delete(r.temporary, name)
	delete(r.persistent, name)
	r.mu.Unlock()
	r.connectionMu.Unlock()
	if !ok {
		return
	}

	closeInBackground(name, client)
	r.notifyToolsChanged()
}

// DisconnectAll disconnects from all MCP servers
func (r *Registry) DisconnectAll() {
	r.connectionMu.Lock()

	r.mu.Lock()
	clients := r.clients
	r.clients = make(map[string]*Client)
	for name := range clients {
		r.generation[name]++
	}
	r.leasing = make(map[string]map[uint64]int)
	r.temporary = make(map[string]bool)
	r.persistent = make(map[string]bool)
	r.mu.Unlock()
	r.connectionMu.Unlock()

	for name, client := range clients {
		closeInBackground(name, client)
	}
	if len(clients) > 0 {
		r.notifyToolsChanged()
	}
}

// closeInBackground tears a client down off the caller's goroutine, reporting
// a failure to the log — the caller cannot act on it, and waiting for it is
// what froze the UI.
func closeInBackground(name string, client *Client) {
	go func() {
		if err := client.Disconnect(); err != nil {
			log.Logger().Warn("mcp: disconnect failed",
				zap.String("server", name), zap.Error(err))
		}
	}()
}

// GetClient returns a client by name
func (r *Registry) GetClient(name string) (*Client, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	client, ok := r.clients[name]
	return client, ok
}

// GetLoader returns the config loader used by this registry.
func (r *Registry) GetLoader() *ConfigLoader {
	return r.loader
}

// GetConfig returns a server config by name
func (r *Registry) GetConfig(name string) (ServerConfig, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	config, ok := r.configs[name]
	return config, ok
}

// List returns all configured servers with their current status
func (r *Registry) List() []Server {
	r.mu.RLock()
	defer r.mu.RUnlock()

	servers := make([]Server, 0, len(r.configs))
	for name, config := range r.configs {
		server := Server{
			Config: config,
			Status: StatusDisconnected,
		}

		if client, ok := r.clients[name]; ok {
			server = client.ToServer()
		} else if r.connecting[name] {
			server.Status = StatusConnecting
		} else if errMsg, ok := r.connectErr[name]; ok {
			server.Status = StatusError
			server.Error = errMsg
		}

		servers = append(servers, server)
	}

	return servers
}

// emptySchema is the default empty JSON schema for tools without input schema
var emptySchema = map[string]any{
	"type":       "object",
	"properties": map[string]any{},
}

// GetToolSchemas returns core.ToolSchema schemas for all connected MCP servers
func (r *Registry) GetToolSchemas() []core.ToolSchema {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var tools []core.ToolSchema
	for serverName, client := range r.clients {
		if !client.IsConnected() {
			continue
		}

		for _, mcpTool := range client.GetCachedTools() {
			tools = append(tools, core.ToolSchema{
				Name:        fmt.Sprintf("mcp__%s__%s", serverName, mcpTool.Name),
				Description: mcpTool.Description,
				Parameters:  parseInputSchema(mcpTool.InputSchema),
			})
		}
	}

	return tools
}

// parseInputSchema parses the input schema or returns a default empty schema
func parseInputSchema(raw json.RawMessage) any {
	if len(raw) == 0 {
		return emptySchema
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return emptySchema
	}
	return schema
}

// CallTool calls a tool on an MCP server
// The tool name should be in the format: mcp__<server>__<tool>
func (r *Registry) CallTool(ctx context.Context, fullName string, arguments map[string]any) (*ToolResult, error) {
	serverName, toolName, ok := parseMCPToolName(fullName)
	if !ok {
		return nil, fmt.Errorf("invalid MCP tool name: %s", fullName)
	}

	client, ok := r.GetClient(serverName)
	if !ok {
		return nil, fmt.Errorf("MCP server not connected: %s", serverName)
	}

	return client.CallTool(ctx, toolName, arguments)
}

// SetOnToolsChanged sets a callback for when tools change
func (r *Registry) SetOnToolsChanged(callback func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onToolsChanged = callback
}

// notifyToolsChanged calls the tools changed callback if set
func (r *Registry) notifyToolsChanged() {
	r.mu.RLock()
	callback := r.onToolsChanged
	r.mu.RUnlock()
	if callback != nil {
		callback()
	}
}

// parseMCPToolName parses a tool name in the format mcp__<server>__<tool>
func parseMCPToolName(name string) (serverName, toolName string, ok bool) {
	rest, found := strings.CutPrefix(name, "mcp__")
	if !found {
		return "", "", false
	}

	serverName, toolName, ok = strings.Cut(rest, "__")
	if !ok || serverName == "" || toolName == "" {
		return "", "", false
	}

	return serverName, toolName, true
}

// IsMCPTool returns true if the tool name is an MCP tool
func IsMCPTool(name string) bool {
	_, _, ok := parseMCPToolName(name)
	return ok
}

// SetConnecting marks or unmarks a server as currently connecting.
// On failure, call SetConnectError to store the error; List() will report StatusError.
func (r *Registry) SetConnecting(name string, val bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if val {
		r.connecting[name] = true
		delete(r.connectErr, name)
	} else {
		delete(r.connecting, name)
	}
}

// SetConnectError stores a connection error for a server that failed to connect.
func (r *Registry) SetConnectError(name string, errMsg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if errMsg != "" {
		r.connectErr[name] = errMsg
	} else {
		delete(r.connectErr, name)
	}
}

// IsDisabled returns whether a server has been explicitly disabled by the user.
func (r *Registry) IsDisabled(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.disabled[name]
}

// SetDisabled sets the disabled state for a server and persists it.
func (r *Registry) SetDisabled(name string, disabled bool) {
	r.mu.Lock()
	if disabled {
		r.disabled[name] = true
	} else {
		delete(r.disabled, name)
	}
	r.mu.Unlock()
	r.saveState()
}

// statePath returns the path to the state file.
func (r *Registry) statePath() string {
	if r.loader != nil {
		return filepath.Join(r.loader.GetProjectDir(), "mcp-state.json")
	}
	return ""
}

// loadState loads persisted disabled state from disk.
func (r *Registry) loadState() {
	path := r.statePath()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var state mcpState
	if err := json.Unmarshal(data, &state); err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, name := range state.Disabled {
		r.disabled[name] = true
	}
}

// saveState persists disabled state to disk.
func (r *Registry) saveState() {
	path := r.statePath()
	if path == "" {
		return
	}
	r.mu.RLock()
	var state mcpState
	for name := range r.disabled {
		state.Disabled = append(state.Disabled, name)
	}
	r.mu.RUnlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = atomicfile.WriteJSON(path, state, 0o644)
}
