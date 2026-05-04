package app

import (
	"github.com/genai-io/gen-code/internal/agent"
	"github.com/genai-io/gen-code/internal/command"
	"github.com/genai-io/gen-code/internal/cron"
	"github.com/genai-io/gen-code/internal/hook"
	"github.com/genai-io/gen-code/internal/identity"
	"github.com/genai-io/gen-code/internal/llm"
	"github.com/genai-io/gen-code/internal/mcp"
	"github.com/genai-io/gen-code/internal/plugin"
	"github.com/genai-io/gen-code/internal/session"
	"github.com/genai-io/gen-code/internal/setting"
	"github.com/genai-io/gen-code/internal/skill"
	"github.com/genai-io/gen-code/internal/subagent"
	"github.com/genai-io/gen-code/internal/task"
	"github.com/genai-io/gen-code/internal/task/tracker"
	"github.com/genai-io/gen-code/internal/tool"
)

// services holds references to domain service singletons, injected into
// model at construction time. Model methods access services through this
// struct instead of calling Default() package-level accessors directly.
type services struct {
	Setting  setting.Service
	LLM      llm.Service
	Tool     tool.Service
	Hook     hook.Service
	Session  session.Service
	Skill    skill.Service
	Subagent subagent.Service
	Command  command.Service
	Task     task.Service
	Tracker  tracker.Service
	Cron     cron.Service
	MCP      mcp.Service
	Plugin   plugin.Service
	Agent    agent.Service
	Identity *identity.Registry
}

func newServices() services {
	return services{
		Setting:  setting.Default(),
		LLM:      llm.Default(),
		Tool:     tool.Default(),
		Hook:     hook.DefaultIfInit(),
		Session:  session.Default(),
		Skill:    skill.Default(),
		Subagent: subagent.Default(),
		Command:  command.Default(),
		Task:     task.Default(),
		Tracker:  tracker.Default(),
		Cron:     cron.Default(),
		MCP:      mcp.Default(),
		Plugin:   plugin.Default(),
		Agent:    agent.Default(),
		Identity: identity.Default(),
	}
}

// refreshAfterReload re-snapshots the 5 services whose singletons are replaced
// by Initialize() calls in ReloadPluginBackedState. The remaining services
// (LLM, Hook, Session, Tool, Task, Tracker, Cron, Plugin)
// are stable — their singletons are created once at startup and never replaced.
func (s *services) refreshAfterReload() {
	s.Setting = setting.Default()
	s.Skill = skill.Default()
	s.Command = command.Default()
	s.Subagent = subagent.Default()
	s.MCP = mcp.Default()
	s.Identity = identity.Default()
}
