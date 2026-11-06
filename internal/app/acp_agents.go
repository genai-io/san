package app

import (
	"context"
	"strings"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/plugin"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/skill"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/tool"
	agenttool "github.com/genai-io/san/internal/tool/agent"
	"github.com/genai-io/san/internal/tool/fs"
	"github.com/genai-io/san/internal/tool/perm"
)

// Rebuild only while idle, after background worker cleanup.
func (s *acpSession) configureAgents() {
	e := subagent.NewExecutor(s.runtimeBuildParams.Provider, s.runtimeBuildParams.CWD, s.runtimeBuildParams.ModelID, s.hooks)
	e.ConfigureOwner(s.runtimeBuildParams.Tasks, s.agents, func(background bool, cwd string) perm.PermissionFunc {
		policy := s.permissionsForDirectory(cwd)
		return func(ctx context.Context, name string, input map[string]any) (bool, string) {
			if name == "Bash" {
				if mode, _ := input["execution_mode"].(string); mode != "" && mode != "foreground" {
					return false, "ACP workers may only run foreground Bash commands"
				}
			}
			if !background {
				if gate := s.runtime.PermissionGate(); gate != nil {
					// Match submitted paths; display resolved paths for approval.
					rules := func(string, map[string]any) agent.PermDecisionResult { return policy.Decide(name, input) }
					return gate.WithRules(rules).Check(ctx, name, setting.PermissionPaths(name, input, cwd), false, "")
				}
				return false, "parent prompt is unavailable"
			}
			decision := policy.Decide(name, input)
			if decision.Decision == perm.Permit {
				return true, decision.Reason
			}
			return false, "headless worker: " + decision.Reason
		}
	}, func(ctx context.Context, source string) context.Context {
		ctx = plugin.WithRegistry(ctx, s.runtimeBuildParams.Plugins)
		ctx = plugin.WithRoot(ctx, s.runtimeBuildParams.Plugins.RootForPath(source))
		ctx = fs.WithShellEnv(ctx, plugin.PluginEnv(ctx))
		ctx = skill.WithRegistry(ctx, s.runtimeBuildParams.Skills)
		return fs.WithFileViews(ctx, s.runtimeBuildParams.FileViews)
	})
	e.SetPostTool(func(result core.ToolResult, effect any) {
		agent.PostTool(result, effect, s.hooks, agent.WorkspaceCallbacks{TouchFile: s.runtimeBuildParams.FileCache.Touch})
	})
	var instructions []string
	for _, file := range system.LoadMemoryFiles(s.runtimeBuildParams.CWD) {
		instructions = append(instructions, file.Content)
	}
	e.SetProjectInstructions(strings.Join(instructions, "\n\n"))
	e.SetSessionStore(s.nativeSession.Store, s.runtimeBuildParams.SessionID)
	e.SetDisabledTools(s.runtimeBuildParams.DisabledTools)
	e.SetMCPDependencies(s.mcp)
	e.SetSkillsDirectory(s.runtimeBuildParams.Skills.PromptSection())
	e.SetSkillResolver(s.runtimeBuildParams.Skills.SkillInvocationPrompt)
	e.SetParentPermissionMode(func() subagent.PermissionMode { return subagent.InheritedPermissionMode(s.permissions.CurrentMode()) })
	worker := agenttool.NewAgentTool()
	worker.SetExecutor(subagent.NewExecutorAdapter(e))
	schema := worker.Schema()
	schema.Description += " ACP supports foreground workers and session-owned background workers. Background requests requiring human approval are denied; use TaskOutput and TaskStop to inspect or cancel."
	// Only foreground workers inherit the prompt responder; RunBackground starts a fresh context.
	s.runtimeBuildParams.NativeTools = append(s.runtimeBuildParams.Tasks.Tools(), tool.AdaptTool(worker, schema, func() string { return s.runtimeBuildParams.CWD }, tool.WithSideEffectHandler(s.runtimeBuildParams.ToolResponse), tool.WithAskUser(s.runtimeBuildParams.AskUser), tool.WithBashPromptResponderProvider(s.runtimeBuildParams.BashPromptResponder)))
	s.runtimeBuildParams.AgentCatalog = s.agents.AgentCatalog
}
