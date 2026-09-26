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
// cleanup function that disconnects only the ones this call connected, so a
// connection the session already had outlives the caller.
//
// ponytail: no refcount — two concurrent callers sharing a server this call
// connected lose it when the first finishes; add per-server counts if that
// case shows up.
func ConnectServers(ctx context.Context, servers *Registry, serverNames []string) (cleanup func(), errs []error) {
	var connected []string
	for _, name := range serverNames {
		if _, ok := servers.GetConfig(name); !ok {
			errs = append(errs, fmt.Errorf("MCP server not configured: %s", name))
			continue
		}
		if c, ok := servers.GetClient(name); ok && c.IsConnected() {
			continue
		}
		if err := servers.Connect(ctx, name); err != nil {
			errs = append(errs, fmt.Errorf("MCP server %s: %w", name, err))
			continue
		}
		connected = append(connected, name)
	}

	cleanup = func() {
		for _, name := range connected {
			servers.Disconnect(name)
		}
	}
	return cleanup, errs
}
