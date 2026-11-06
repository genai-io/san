package mcp

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"strings"
)

var serverName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// NewSession loads native configuration and adds connection-local contributions.
// Conflicting names fail rather than redirecting an existing permission rule.
// Nothing is persisted or installed as the process default.
func NewSession(cwd string, plugins []PluginServer, supplied map[string]ServerConfig) (*Manager, error) {
	r, err := NewManager(cwd)
	if err != nil {
		return nil, err
	}
	r.PluginServers = func() []PluginServer { return plugins }
	r.configs = r.mergePluginMCPConfigs(r.configs)
	for name, cfg := range supplied {
		if _, exists := r.configs[name]; exists {
			return nil, fmt.Errorf("MCP server name conflicts with native configuration: %s", name)
		}
		cfg.Name = name
		cfg.Args = append([]string(nil), cfg.Args...)
		cfg.Env, cfg.Headers = maps.Clone(cfg.Env), maps.Clone(cfg.Headers)
		r.configs[name] = cfg
	}
	for name, cfg := range r.configs {
		if !serverName.MatchString(name) || strings.Contains(name, "__") {
			return nil, fmt.Errorf("invalid MCP server name: %s", name)
		}
		cfg.CWD = cwd
		r.configs[name] = cfg
	}
	return r, nil
}

// ConnectAll is synchronous; the owner must join it before Close. Failure closes
// all partially opened connections. Errors omit server output and credentials.
func (r *Manager) ConnectAll(ctx context.Context) error {
	for _, server := range r.List() {
		if r.IsDisabled(server.Config.Name) {
			continue
		}
		if err := r.Connect(ctx, server.Config.Name); err != nil {
			return errors.Join(fmt.Errorf("MCP server %s failed to connect", server.Config.Name), r.Close())
		}
	}
	return nil
}

// Close joins owned clients. Call only after connects and tool calls have joined.
func (r *Manager) Close() error {
	r.mu.Lock()
	clients := r.clients
	r.clients = make(map[string]*Client)
	r.mu.Unlock()
	var err error
	for _, client := range clients {
		err = errors.Join(err, client.Disconnect())
	}
	return err
}
