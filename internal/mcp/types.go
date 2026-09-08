// Package mcp manages the MCP servers San is configured with and exposes what
// they advertise to the agent loop. Reaching one, and the protocol spoken to it,
// are sdk-go's — see client.go.
package mcp

import "encoding/json"

// TransportType defines the type of MCP transport
type TransportType string

const (
	TransportSTDIO TransportType = "stdio"
	TransportHTTP  TransportType = "http"
	TransportSSE   TransportType = "sse"
)

// Scope defines where the MCP configuration is stored
type Scope string

const (
	ScopeUser    Scope = "user"    // ~/.san/mcp.json (global)
	ScopeProject Scope = "project" // ./.san/mcp.json (team shared)
	ScopeLocal   Scope = "local"   // ./.san/mcp.local.json (personal, git-ignored)
)

// ServerConfig represents an MCP server configuration
type ServerConfig struct {
	// Name is the unique identifier for this server
	Name string `json:"name,omitempty"`

	// Type is the transport type (stdio, http, sse). Default is stdio.
	Type TransportType `json:"type,omitempty"`

	// STDIO transport fields
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`

	// HTTP/SSE transport fields
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`

	// Scope indicates where this config was loaded from
	Scope Scope `json:"-"`
}

// GetType returns the transport type, defaulting to stdio if not set
func (c *ServerConfig) GetType() TransportType {
	if c.Type == "" {
		if c.URL != "" {
			return TransportHTTP
		}
		return TransportSTDIO
	}
	return c.Type
}

// MCPConfig represents the mcp.json configuration file format
type MCPConfig struct {
	MCPServers map[string]ServerConfig `json:"mcpServers"`
}

// MCPTool represents a tool exposed by an MCP server
type MCPTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// MCPResource represents a resource exposed by an MCP server
type MCPResource struct {
	URI         string `json:"uri"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// MCPPrompt represents a prompt template exposed by an MCP server
type MCPPrompt struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ToolResult represents the result of calling an MCP tool
type ToolResult struct {
	Content []ToolResultContent `json:"content,omitempty"`
	IsError bool                `json:"isError,omitempty"`
}

// ToolResultContent represents a content item in a tool result
type ToolResultContent struct {
	Type string `json:"type"` // "text", "image", "resource"
	Text string `json:"text,omitempty"`
	// Additional fields for other content types
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// ServerStatus represents the current status of an MCP server connection
type ServerStatus string

const (
	StatusDisconnected ServerStatus = "disconnected"
	StatusConnecting   ServerStatus = "connecting"
	StatusConnected    ServerStatus = "connected"
	StatusError        ServerStatus = "error"
)

// Server represents a connected MCP server with its current state
type Server struct {
	Config    ServerConfig  `json:"config"`
	Status    ServerStatus  `json:"status"`
	Error     string        `json:"error,omitempty"`
	Tools     []MCPTool     `json:"tools,omitempty"`
	Resources []MCPResource `json:"resources,omitempty"`
	Prompts   []MCPPrompt   `json:"prompts,omitempty"`
}
