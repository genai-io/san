package app

import (
	"context"
	"strings"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/plugin"
	"github.com/genai-io/san/internal/skill"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/tool"
	agenttool "github.com/genai-io/san/internal/tool/agent"
	"github.com/genai-io/san/internal/tool/fs"
	"github.com/genai-io/san/internal/tool/perm"
)

// Rebuilt only with the idle parent runtime. Configuration changes are rejected
// until background workers finish cleanup, preserving their policy and hooks.
func (s *acpSession) configureAgents() {
	e := subagent.NewExecutor(s.params.Provider, s.params.CWD, s.params.ModelID, s.hooks)
	e.ConfigureOwner(s.params.Tasks, s.agents, func(background bool) perm.PermissionFunc {
		return func(ctx context.Context, name string, input map[string]any) (bool, string) {
			if name == "Bash" {
				if mode, _ := input["execution_mode"].(string); mode != "" && mode != "foreground" {
					return false, "ACP workers may only run foreground Bash commands"
				}
			}
			if !background {
				if gate := s.runtime.PermissionGate(); gate != nil {
					return gate.Check(ctx, name, input, false, "")
				}
				return false, "parent prompt is unavailable"
			}
			decision := s.params.PermissionRules(name, input)
			if decision.Decision == perm.Permit {
				return true, decision.Reason
			}
			return false, "headless worker: " + decision.Reason
		}
	}, func(ctx context.Context, source string) context.Context {
		ctx = plugin.WithRegistry(ctx, s.params.Plugins)
		ctx = plugin.WithRoot(ctx, s.params.Plugins.RootForPath(source))
		ctx = fs.WithShellEnv(ctx, plugin.PluginEnv(ctx))
		ctx = skill.WithRegistry(ctx, s.params.Skills)
		return fs.WithFileViews(ctx, s.params.FileViews)
	})
	e.SetPostTool(func(result core.ToolResult, effect any) {
		agent.PostTool(result, effect, s.hooks, agent.WorkspaceCallbacks{TouchFile: s.params.FileCache.Touch})
	})
	var instructions []string
	for _, file := range system.LoadMemoryFiles(s.params.CWD) {
		instructions = append(instructions, file.Content)
	}
	e.SetProjectInstructions(strings.Join(instructions, "\n\n"))
	e.SetSessionStore(s.data.Store, s.params.SessionID)
	e.SetDisabledTools(s.params.DisabledTools)
	e.SetMCPDependencies(s.mcp)
	e.SetSkillsDirectory(s.params.Skills.PromptSection())
	e.SetParentPermissionMode(func() subagent.PermissionMode { return subagent.InheritedPermissionMode(s.policy.CurrentMode()) })
	worker := agenttool.NewAgentTool()
	worker.SetExecutor(subagent.NewExecutorAdapter(e))
	schema := worker.Schema()
	schema.Description += " ACP supports foreground workers and session-owned background workers. Background requests requiring human approval are denied; use TaskOutput and TaskStop to inspect or cancel."
	// Foreground workers inherit the prompt-owned responder through their context.
	// RunBackground starts with a fresh context and cannot retain this channel.
	s.params.NativeTools = append(s.params.Tasks.Tools(), tool.AdaptTool(worker, schema, func() string { return s.params.CWD }, tool.WithAskUser(s.params.AskUser), tool.WithBashPromptResponderProvider(s.params.BashPromptResponder)))
	s.params.AgentCatalog = s.agents.AgentCatalog
}
