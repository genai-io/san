package app

import (
	"slices"

	"github.com/genai-io/san/internal/command"
	"github.com/genai-io/san/internal/mcp"
	"github.com/genai-io/san/internal/plugin"
	"github.com/genai-io/san/internal/skill"
	"github.com/genai-io/san/internal/subagent"
)

type pluginSource struct {
	registry *plugin.Registry
}

func isProjectPlugin(scope plugin.Scope) bool {
	return scope == plugin.ScopeProject || scope == plugin.ScopeLocal
}

func (src pluginSource) commandPaths() []command.PluginCommandPath {
	var paths []command.PluginCommandPath
	for _, p := range src.registry.CommandPaths() {
		paths = append(paths, command.PluginCommandPath{Path: p.Path, Namespace: p.Namespace, IsProject: isProjectPlugin(p.Scope)})
	}
	return paths
}

func (src pluginSource) skillPaths() []skill.PluginSkillPath {
	var paths []skill.PluginSkillPath
	for _, p := range src.registry.SkillPaths() {
		paths = append(paths, skill.PluginSkillPath{Path: p.Path, Namespace: p.Namespace, IsProject: isProjectPlugin(p.Scope)})
	}
	return paths
}

func (src pluginSource) agentPaths() []subagent.PluginAgentPath {
	var paths []subagent.PluginAgentPath
	for _, p := range src.registry.AgentPaths() {
		paths = append(paths, subagent.PluginAgentPath{Path: p.Path, Namespace: p.Namespace})
	}
	return paths
}

func (src pluginSource) mcpServers() []mcp.PluginServer {
	var servers []mcp.PluginServer
	for _, s := range src.registry.MCPServers() {
		servers = append(servers, mcp.PluginServer{
			Name: s.Name, Type: s.Config.Type, Scope: string(s.Scope),
			Command: s.Config.Command, Args: slices.Clone(s.Config.Args), Env: s.Config.Env,
			URL: s.Config.URL, Headers: s.Config.Headers,
		})
	}
	return servers
}
