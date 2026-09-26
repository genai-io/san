package mcp

import (
	"context"
	"fmt"
	"strings"
)

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
func ConnectServers(ctx context.Context, servers *Manager, serverNames []string) (cleanup func(), errs []error) {
	var held []string
	for _, name := range serverNames {
		if _, ok := servers.GetConfig(name); !ok {
			errs = append(errs, fmt.Errorf("MCP server not configured: %s", name))
			continue
		}
		ok, err := servers.hold(ctx, name)
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

// hold takes a hold on name, connecting it if nothing is connected yet. It
// reports false for a server the session connected itself, which no caller
// holds and none may disconnect.
func (r *Manager) hold(ctx context.Context, name string) (bool, error) {
	r.holdersMu.Lock()
	defer r.holdersMu.Unlock()
	if r.holders[name] == 0 {
		if c, ok := r.GetClient(name); ok && c.IsConnected() {
			return false, nil
		}
		if err := r.Connect(ctx, name); err != nil {
			return false, err
		}
		if r.holders == nil {
			r.holders = make(map[string]int)
		}
	}
	r.holders[name]++
	return true, nil
}

// release drops one hold on name and disconnects it with the last one.
func (r *Manager) release(name string) {
	r.holdersMu.Lock()
	defer r.holdersMu.Unlock()
	r.holders[name]--
	if r.holders[name] > 0 {
		return
	}
	delete(r.holders, name)
	r.Disconnect(name)
}
