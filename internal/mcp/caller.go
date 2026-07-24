package mcp

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Caller adapts the mcp.Tools surface into the (content, isError, err)
// tuple shape the agent loop expects.
type Caller struct {
	tools Tools
}

// NewCaller wraps a Tools implementation in the *Caller helper consumed
// by AsCoreTools. Typically called with *Registry; tests may pass a
// fake Tools.
func NewCaller(tools Tools) *Caller {
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

// ConnectServers acquires a connection lease for each configured MCP server.
// Concurrent custom-Agent runs share one connection; cleanup releases only this
// invocation's leases, and a connection that existed beforehand stays connected.
func ConnectServers(ctx context.Context, servers *Registry, serverNames []string) (cleanup func(), errs []error) {
	type lease struct {
		name       string
		generation uint64
	}
	var acquired []lease
	for _, name := range serverNames {
		generation, err := servers.acquireServer(ctx, name)
		if err != nil {
			errs = append(errs, fmt.Errorf("MCP server %s: %w", name, err))
			continue
		}
		acquired = append(acquired, lease{name: name, generation: generation})
	}

	var once sync.Once
	cleanup = func() {
		once.Do(func() {
			for _, lease := range acquired {
				servers.releaseServer(lease.name, lease.generation)
			}
		})
	}
	return cleanup, errs
}
