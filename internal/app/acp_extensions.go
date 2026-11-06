package app

import (
	"context"
	"fmt"

	"github.com/genai-io/san/internal/mcp"
	"github.com/genai-io/san/internal/plugin"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/skill"
	"github.com/genai-io/sdk-go/pkg/acp/protocol"
)

// Load extension registries per session; never swap the TUI's defaults.
func acpExtensions(ctx context.Context, cwd string, settings *setting.Data, supplied []protocol.MCPServer) (*plugin.Registry, *skill.Registry, *mcp.Manager, error) {
	plugins := plugin.NewRegistry()
	if err := plugins.Load(ctx, cwd); err != nil {
		return nil, nil, nil, err
	}
	plugins.MergeHooksIntoSettings(settings)
	skills := skill.Load(skill.Options{CWD: cwd, PluginSkillPaths: func() []skill.PluginSkillPath {
		var paths []skill.PluginSkillPath
		for _, p := range plugins.SkillPaths() {
			paths = append(paths, skill.PluginSkillPath{Path: p.Path, Namespace: p.Namespace, IsProject: p.Scope != plugin.ScopeUser})
		}
		return paths
	}})
	configs, err := acpMCPConfigs(supplied)
	if err != nil {
		return nil, nil, nil, err
	}
	var contributions []mcp.PluginServer
	for _, p := range plugins.MCPServers() {
		c := p.Config
		contributions = append(contributions, mcp.PluginServer{Name: p.Name, Type: c.Type, Command: c.Command, Args: c.Args, Env: c.Env, URL: c.URL, Headers: c.Headers, Scope: string(p.Scope)})
	}
	servers, err := mcp.NewSession(cwd, contributions, configs)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := servers.ConnectAll(ctx); err != nil {
		return nil, nil, nil, err
	}
	return plugins, skills, servers, nil
}

func acpMCPConfigs(servers []protocol.MCPServer) (map[string]mcp.ServerConfig, error) {
	configs := make(map[string]mcp.ServerConfig)
	for _, server := range servers {
		var c mcp.ServerConfig
		switch s := server.Value.(type) {
		case protocol.MCPServerStdioVariant:
			c = mcp.ServerConfig{Name: s.Name, Type: mcp.TransportSTDIO, Command: s.Command, Args: s.Args, Env: make(map[string]string)}
			if s.Command == "" {
				return nil, fmt.Errorf("MCP command is required")
			}
			for _, e := range s.Env {
				if _, exists := c.Env[e.Name]; exists {
					return nil, fmt.Errorf("duplicate MCP environment name")
				}
				c.Env[e.Name] = e.Value
			}
		case protocol.MCPServerHTTPVariant:
			c = mcp.ServerConfig{Name: s.Name, Type: mcp.TransportHTTP, URL: s.URL, Headers: make(map[string]string)}
			for _, h := range s.Headers {
				c.Headers[h.Name] = h.Value
			}
		case protocol.MCPServerSSEVariant:
			c = mcp.ServerConfig{Name: s.Name, Type: mcp.TransportSSE, URL: s.URL, Headers: make(map[string]string)}
			for _, h := range s.Headers {
				c.Headers[h.Name] = h.Value
			}
		default:
			return nil, fmt.Errorf("unsupported MCP transport")
		}
		if _, exists := configs[c.Name]; exists {
			return nil, fmt.Errorf("duplicate MCP server name")
		}
		configs[c.Name] = c
	}
	return configs, nil
}
