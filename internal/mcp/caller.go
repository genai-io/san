package mcp

import (
	"context"
	"fmt"
	"strings"
)

// Caller adapts the registry's CallTool into the (content, isError, err)
// tuple shape the agent loop expects.
type Caller struct {
	tools *Registry
}

// NewCaller wraps a registry in the *Caller helper consumed by AsCoreTools.
func NewCaller(tools *Registry) *Caller {
	return &Caller{tools: tools}
}

// IsMCPTool returns true if the name is an MCP tool (mcp__*__*).
func (c *Caller) IsMCPTool(name string) bool {
	return IsMCPTool(name)
}

// CallTool calls an MCP tool and returns the content string and error status.
func (c *Caller) CallTool(ctx context.Context, fullName string, arguments map[string]any) (string, bool, error) {
	result, err := c.tools.CallTool(ctx, fullName, arguments)
	if err != nil {
		return "", false, err
	}

	content := ExtractContent(result.Content)
	return content, result.IsError, nil
}

// ExtractContent extracts text content from MCP tool result.
func ExtractContent(contents []ToolResultContent) string {
	var parts []string
	for _, c := range contents {
		if c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// ConnectServers connects to a specific set of MCP servers. Returns a
// cleanup function that releases this call's hold on the ones it connected; a
// server disconnects once its last holder releases it, so a connection the
// session already had outlives every caller.
func ConnectServers(ctx context.Context, servers *Registry, serverNames []string) (cleanup func(), errs []error) {
	var held []string
	for _, name := range serverNames {
		if _, ok := servers.GetConfig(name); !ok {
			errs = append(errs, fmt.Errorf("MCP server not configured: %s", name))
			continue
		}
		ok, err := servers.acquire(ctx, name)
		if err != nil {
			errs = append(errs, fmt.Errorf("MCP server %s: %w", name, err))
			continue
		}
		if ok {
			held = append(held, name)
		}
	}

	cleanup = func() {
		for _, name := range held {
			servers.release(name)
		}
	}
	return cleanup, errs
}

// acquire takes a hold on name, connecting it if nothing is connected yet. It
// reports false for a server the session connected itself, which no caller
// holds and none may disconnect.
func (r *Registry) acquire(ctx context.Context, name string) (bool, error) {
	r.leaseMu.Lock()
	defer r.leaseMu.Unlock()
	if r.leases[name] == 0 {
		if c, ok := r.GetClient(name); ok && c.IsConnected() {
			return false, nil
		}
		if err := r.Connect(ctx, name); err != nil {
			return false, err
		}
		if r.leases == nil {
			r.leases = make(map[string]int)
		}
	}
	r.leases[name]++
	return true, nil
}

// release drops one hold on name and disconnects it with the last one.
func (r *Registry) release(name string) {
	r.leaseMu.Lock()
	defer r.leaseMu.Unlock()
	r.leases[name]--
	if r.leases[name] > 0 {
		return
	}
	delete(r.leases, name)
	r.Disconnect(name)
}
