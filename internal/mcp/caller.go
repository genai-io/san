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

// AcquireServerConnectionLeases acquires a connection lease for each configured
// MCP server. Concurrent custom-Agent runs share one connection; releaseLeases
// releases only this invocation's leases, and a preexisting connection remains.
func AcquireServerConnectionLeases(ctx context.Context, registry *Registry, serverNames []string) (releaseLeases func(), acquireErrors []error) {
	type connectionLeaseToken struct {
		serverName      string
		connectionEpoch uint64
	}

	acquiredLeaseTokens := make([]connectionLeaseToken, 0, len(serverNames))
	for _, serverName := range serverNames {
		connectionEpoch, err := registry.acquireConnectionLease(ctx, serverName)
		if err != nil {
			acquireErrors = append(acquireErrors, fmt.Errorf("MCP server %s: %w", serverName, err))
			continue
		}
		acquiredLeaseTokens = append(acquiredLeaseTokens, connectionLeaseToken{
			serverName:      serverName,
			connectionEpoch: connectionEpoch,
		})
	}

	var releaseOnce sync.Once
	releaseLeases = func() {
		releaseOnce.Do(func() {
			for _, leaseToken := range acquiredLeaseTokens {
				registry.releaseConnectionLease(leaseToken.serverName, leaseToken.connectionEpoch)
			}
		})
	}
	return releaseLeases, acquireErrors
}
