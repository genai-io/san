// Package host holds wiring shared by San hosts (TUI, print mode, headless agent and ACP).
package host

import (
	"slices"

	"github.com/genai-io/san/internal/command"
	"github.com/genai-io/san/internal/mcp"
	"github.com/genai-io/san/internal/plugin"
	"github.com/genai-io/san/internal/skill"
	"github.com/genai-io/san/internal/subagent"
)

// PluginSource adapts plugin registry paths for the extension loaders.
type PluginSource struct {
	Registry *plugin.Registry
}

func isProjectPlugin(scope plugin.Scope) bool {
	return scope == plugin.ScopeProject || scope == plugin.ScopeLocal
}

func (src PluginSource) CommandPaths() []command.PluginCommandPath {
	var paths []command.PluginCommandPath
	for _, p := range src.Registry.CommandPaths() {
		paths = append(paths, command.PluginCommandPath{Path: p.Path, Namespace: p.Namespace, IsProject: isProjectPlugin(p.Scope)})
	}
	return paths
}

func (src PluginSource) SkillPaths() []skill.PluginSkillPath {
	var paths []skill.PluginSkillPath
	for _, p := range src.Registry.SkillPaths() {
		paths = append(paths, skill.PluginSkillPath{Path: p.Path, Namespace: p.Namespace, IsProject: isProjectPlugin(p.Scope)})
	}
	return paths
}

func (src PluginSource) AgentPaths() []subagent.PluginAgentPath {
	var paths []subagent.PluginAgentPath
	for _, p := range src.Registry.AgentPaths() {
		paths = append(paths, subagent.PluginAgentPath{Path: p.Path, Namespace: p.Namespace})
	}
	return paths
}

func (src PluginSource) MCPServers() []mcp.PluginServer {
	var servers []mcp.PluginServer
	for _, s := range src.Registry.MCPServers() {
		servers = append(servers, mcp.PluginServer{
			Name: s.Name, Type: s.Config.Type, Scope: string(s.Scope),
			Command: s.Config.Command, Args: slices.Clone(s.Config.Args), Env: s.Config.Env,
			URL: s.Config.URL, Headers: s.Config.Headers,
		})
	}
	return servers
}
