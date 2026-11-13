package app

import (
	"context"
	"strings"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/plugin"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/skill"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/tool"
	agenttool "github.com/genai-io/san/internal/tool/agent"
	"github.com/genai-io/san/internal/tool/fs"
	"github.com/genai-io/san/internal/tool/perm"
)

type acpWorkerResolver struct {
	resolve func(context.Context, string, map[string]any, bool, string) perm.Resolution
}

func (r acpWorkerResolver) Resolve(ctx context.Context, name string, input map[string]any, force bool, reason string) perm.Resolution {
	return r.resolve(ctx, name, input, force, reason)
}

// Rebuild only while idle, after background worker cleanup.
func (s *acpSession) configureAgents() error {
	e := subagent.NewExecutor(s.runtimeBuildParams.Provider, s.runtimeBuildParams.CWD, s.runtimeBuildParams.ModelID, s.hooks)
	e.ConfigureOwner(s.runtimeBuildParams.Tasks, s.agents, func(child *session.Session, background bool, source tool.AgentInteractionSource) tool.Resolver {
		cwd := source.WorkingDirectory
		policy := s.permissions.ForDirectory(cwd)
		return acpWorkerResolver{resolve: func(ctx context.Context, name string, input map[string]any, force bool, reason string) perm.Resolution {
			if name == "Bash" {
				if mode, _ := input["execution_mode"].(string); mode != "" && mode != "foreground" {
					return perm.Resolution{Decision: "deny", By: "config", Reason: "ACP workers may only run foreground Bash commands"}
				}
			}
			if !background {
				if gate := s.runtime.PermissionGate(); gate != nil {
					rules := func(string, map[string]any) agent.PermDecisionResult { return policy.Decide(name, input) }
					return gate.WithRules(rules).Resolve(ctx, name, setting.PermissionPaths(name, input, cwd), force, reason)
				}
				return perm.Resolution{Decision: "deny", By: "config", Reason: "parent prompt is unavailable"}
			}
			decision := policy.Decide(name, input)
			if !force && decision.Decision == perm.Permit {
				return perm.Resolution{Decision: "allow", By: decision.By, Scope: decision.Scope, Reason: decision.Reason}
			}
			if decision.Decision == perm.Reject {
				return perm.Resolution{Decision: "deny", By: decision.By, Scope: decision.Scope, Reason: decision.Reason}
			}
			return perm.Resolution{Decision: "deny", By: "config", Reason: "headless worker: " + decision.Reason}
		}}
	}, func(ctx context.Context, source string) context.Context {
		ctx = plugin.WithRegistry(ctx, s.runtimeBuildParams.Plugins)
		ctx = plugin.WithRoot(ctx, s.runtimeBuildParams.Plugins.RootForPath(source))
		ctx = fs.WithShellEnvProvider(ctx, plugin.PluginEnv)
		ctx = skill.WithRegistry(ctx, s.runtimeBuildParams.Skills)
		return fs.WithFileViews(ctx, s.runtimeBuildParams.FileViews)
	})
	e.SetChildHooks(func(owner *session.Session, provider llm.Provider, model string) (hook.Handler, func(llm.Provider, string), func()) {
		projection := owner.Snapshot()
		cwd := projection.Identity().InitialCWD
		if saved := projection.State().ExecutionCWD; saved != nil {
			cwd = *saved
		}
		child := s.hooks.ForSession(owner.ID(), cwd, owner.Path())
		bindSessionHooks(child, provider, owner, model)
		child.SetEnvProvider(func(ctx context.Context) []string {
			return plugin.PluginEnv(plugin.WithRegistry(ctx, s.runtimeBuildParams.Plugins))
		})
		return child, func(provider llm.Provider, model string) { bindSessionHooks(child, provider, owner, model) }, child.Shutdown
	})
	e.SetPostTool(func(handler hook.Handler, result core.ToolResult, effect any) error {
		engine, _ := handler.(*hook.Engine)
		agent.PostTool(result, effect, engine, agent.WorkspaceCallbacks{TouchFile: s.runtimeBuildParams.FileCache.Touch})
		return nil
	})
	var instructions []string
	for _, file := range system.LoadMemoryFiles(s.runtimeBuildParams.CWD) {
		instructions = append(instructions, file.Content)
	}
	e.SetProjectInstructions(strings.Join(instructions, "\n\n"))
	if err := e.SetSessions(s.nativeSession.Store, s.nativeSession.Session); err != nil {
		return err
	}
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
	return nil
}
