package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	sdkmcp "github.com/genai-io/sdk-go/pkg/agent/mcp"
	"github.com/genai-io/sdk-go/pkg/ai"

	"github.com/genai-io/san/internal/core"
	glog "github.com/genai-io/san/internal/log"
)

// One MCP server, and San's business with it: when a connection is made and
// dropped, and what the /mcp listing shows. The protocol is sdk-go's.

// conn is one live session. The real one is the SDK's; the registry's tests
// supply their own, since leases and epochs are what they are about.
type conn interface {
	Tools(ctx context.Context) ([]core.Tool, error)
	Resources(ctx context.Context) ([]sdkmcp.Resource, error)
	Prompts(ctx context.Context) ([]sdkmcp.Prompt, error)
	Alive() bool
	Close() error
}

// Client is one MCP server as San holds it.
type Client struct {
	config ServerConfig

	// dial opens the session. Tests replace it; see conn.
	dial func(ctx context.Context, onToolsChanged func()) (conn, error)

	mu        sync.RWMutex
	session   conn
	tools     []core.Tool
	resources []MCPResource
	prompts   []MCPPrompt

	onToolsChanged func()
}

// NewClient returns a client for one server. Nothing is reached until Connect.
func NewClient(config ServerConfig) *Client {
	return &Client{config: config, dial: dialSDK(config)}
}

// dialSDK opens the real session. Tool names come back unqualified: the
// registry assembles mcp__server__tool, where San has always matched on it.
func dialSDK(config ServerConfig) func(context.Context, func()) (conn, error) {
	return func(ctx context.Context, onToolsChanged func()) (conn, error) {
		server := sdkmcp.Server{
			Command: config.Command,
			Args:    config.Args,
			Env:     config.Env,
			URL:     config.URL,
			Headers: config.Headers,
			SSE:     config.GetType() == TransportSSE,
			// San is full-screen, so this cannot go to the terminal.
			Stderr: serverLog{name: config.Name},
		}
		var opts []sdkmcp.Option
		if onToolsChanged != nil {
			// The SDK hands the callback its own client; San's re-read goes
			// through this one, which is the same session under San's cache.
			opts = append(opts, sdkmcp.OnToolsChanged(func(*sdkmcp.Client) { onToolsChanged() }))
		}
		c, err := sdkmcp.Connect(ctx, server, opts...)
		if err != nil {
			return nil, err
		}
		return sdkSession{c}, nil
	}
}

// serverLog is one server's stderr, as a line in San's log.
type serverLog struct{ name string }

func (l serverLog) Write(p []byte) (int, error) {
	if line := strings.TrimRight(string(p), "\n"); line != "" {
		glog.Logger().Debug("mcp server output", zap.String("server", l.name), zap.String("line", line))
	}
	return len(p), nil
}

// sdkSession is the SDK's client under what this package asks.
type sdkSession struct{ *sdkmcp.Client }

func (s sdkSession) Tools(ctx context.Context) ([]core.Tool, error) {
	return s.Client.Tools(ctx)
}

// Connect opens the session and reads what the server offers. Already
// connected is a no-op, not a second process.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	if c.session != nil && c.session.Alive() {
		c.mu.Unlock()
		return nil
	}
	dial := c.dial
	c.mu.Unlock()

	session, err := dial(ctx, c.notifyToolsChanged)
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.session = session
	c.mu.Unlock()

	if err := c.refresh(ctx); err != nil {
		_ = c.Disconnect()
		return err
	}
	return nil
}

// refresh re-reads what the server offers, outside the lock so a slow server
// does not block whoever is asking whether this one is connected.
func (c *Client) refresh(ctx context.Context) error {
	session := c.conn()
	if session == nil {
		return fmt.Errorf("not connected")
	}
	tools, err := session.Tools(ctx)
	if err != nil {
		return err
	}
	// A server need not offer resources or prompts, and saying so is not a
	// failure worth dropping the connection over.
	resources, _ := session.Resources(ctx)
	prompts, _ := session.Prompts(ctx)

	c.mu.Lock()
	c.tools = tools
	c.resources = toMCPResources(resources)
	c.prompts = toMCPPrompts(prompts)
	c.mu.Unlock()
	return nil
}

// Disconnect ends the session; not connected is a no-op.
func (c *Client) Disconnect() error {
	c.mu.Lock()
	session := c.session
	c.session, c.tools, c.resources, c.prompts = nil, nil, nil, nil
	c.mu.Unlock()

	if session == nil {
		return nil
	}
	return session.Close()
}

// IsConnected reports whether the session is up.
func (c *Client) IsConnected() bool {
	session := c.conn()
	return session != nil && session.Alive()
}

func (c *Client) conn() conn {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.session
}

// GetCachedTools is what the server last said it offers, so the /mcp listing
// and the tool picker never block on one.
func (c *Client) GetCachedTools() []MCPTool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make([]MCPTool, 0, len(c.tools))
	for _, t := range c.tools {
		schema := t.Schema()
		tool := MCPTool{Name: schema.Name, Description: schema.Description}
		if raw, err := json.Marshal(schema.Definition); err == nil {
			tool.InputSchema = raw
		}
		out = append(out, tool)
	}
	return out
}

// CallTool runs one of this server's tools, by the server's own name for it.
func (c *Client) CallTool(ctx context.Context, name string, arguments map[string]any) (*ToolResult, error) {
	tool, ok := c.tool(name)
	if !ok {
		if !c.IsConnected() {
			return nil, fmt.Errorf("not connected")
		}
		return nil, fmt.Errorf("MCP server %s does not offer %s", c.config.Name, name)
	}

	input, err := json.Marshal(arguments)
	if err != nil {
		return nil, err
	}

	// A tool that failed is not a failed call: the model is told what the
	// server said, so it can correct itself.
	result, runErr := tool.Run(ctx, ai.ToolCall{Name: name, Input: string(input)})
	out := &ToolResult{Content: toolResultContent(result.Content), IsError: runErr != nil}
	if runErr != nil && len(out.Content) == 0 {
		out.Content = []ToolResultContent{{Type: "text", Text: runErr.Error()}}
	}
	return out, nil
}

func (c *Client) tool(name string) (core.Tool, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, t := range c.tools {
		if t.Schema().Name == name {
			return t, true
		}
	}
	return nil, false
}

// toolResultContent is what the server returned, in the shape San draws. A
// block it cannot draw is named rather than dropped.
func toolResultContent(content ai.Content) []ToolResultContent {
	out := make([]ToolResultContent, 0, len(content))
	for _, b := range content {
		switch b.Type {
		case ai.BlockText:
			out = append(out, ToolResultContent{Type: "text", Text: b.Text})
		case ai.BlockImage:
			if b.Image != nil {
				out = append(out, ToolResultContent{Type: "image", Data: b.Image.Data, MimeType: b.Image.MediaType})
			}
		default:
			out = append(out, ToolResultContent{Type: "text", Text: fmt.Sprintf("(%s)", b.Type)})
		}
	}
	return out
}

func toMCPResources(in []sdkmcp.Resource) []MCPResource {
	out := make([]MCPResource, 0, len(in))
	for _, r := range in {
		out = append(out, MCPResource{URI: r.URI, Name: r.Name, Description: r.Description, MimeType: r.MediaType})
	}
	return out
}

func toMCPPrompts(in []sdkmcp.Prompt) []MCPPrompt {
	out := make([]MCPPrompt, 0, len(in))
	for _, p := range in {
		out = append(out, MCPPrompt{Name: p.Name, Description: p.Description})
	}
	return out
}

// SetOnToolsChanged installs the callback for a tool list that changed. Before
// or after Connect both work.
func (c *Client) SetOnToolsChanged(callback func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onToolsChanged = callback
}

// toolsChangedTimeout bounds the re-read a change notification triggers: a
// server that announces a change and then stops answering leaks a goroutine.
const toolsChangedTimeout = 30 * time.Second

// notifyToolsChanged re-reads the tools, then tells whoever asked — otherwise
// every consumer asks again at once. A failed re-read tells no one, leaving the
// last good list in place.
func (c *Client) notifyToolsChanged() {
	ctx, cancel := context.WithTimeout(context.Background(), toolsChangedTimeout)
	defer cancel()
	if err := c.refresh(ctx); err != nil {
		return
	}
	c.mu.RLock()
	callback := c.onToolsChanged
	c.mu.RUnlock()
	if callback != nil {
		callback()
	}
}

// ToServer is this server as /mcp shows it.
func (c *Client) ToServer() Server {
	c.mu.RLock()
	status := c.statusLocked()
	resources := make([]MCPResource, len(c.resources))
	copy(resources, c.resources)
	prompts := make([]MCPPrompt, len(c.prompts))
	copy(prompts, c.prompts)
	c.mu.RUnlock()

	return Server{
		Config:    c.config,
		Status:    status,
		Tools:     c.GetCachedTools(),
		Resources: resources,
		Prompts:   prompts,
	}
}

// statusLocked separates a client that never connected from one whose session
// has died, which is worth a reconnect.
func (c *Client) statusLocked() ServerStatus {
	switch {
	case c.session == nil:
		return StatusDisconnected
	case c.session.Alive():
		return StatusConnected
	}
	return StatusError
}
