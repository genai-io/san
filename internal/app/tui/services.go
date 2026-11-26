package tui

import (
	"context"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/agent/reminder"
	"github.com/genai-io/san/internal/command"
	"github.com/genai-io/san/internal/cron"
	"github.com/genai-io/san/internal/flow"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/mcp"
	"github.com/genai-io/san/internal/persona"
	"github.com/genai-io/san/internal/plugin"
	reflectskill "github.com/genai-io/san/internal/reflect/skill"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/skill"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/todo"
	"github.com/genai-io/san/internal/tool"
)

// services holds references to domain service singletons, injected into
// model at construction time. Model methods access services through this
// struct instead of calling Default() package-level accessors directly.
type services struct {
	Setting      *setting.Settings
	LLM          *llm.Conn
	Tool         *tool.Registry
	Hook         *hook.Engine
	Session      *session.Session
	SessionStore *session.Store
	sessionID    string
	Skill        *skill.Registry
	Subagent     *subagent.Registry
	Command      *command.Registry
	Task         *task.Manager
	Flow         *flow.Service
	Tracker      *todo.Store
	Cron         *cron.Scheduler
	MCP          *mcp.Manager
	Plugin       *plugin.Registry
	Agent        *agent.Runtime
	Persona      *persona.Registry
	Reminder     *reminder.Queue

	// AutoSkill owns the reviewer for the current conversation session.
	AutoSkill AutoSkillServices
}

type AutoSkillServices struct {
	session *autoSkillSession
}

type autoSkillSession struct {
	reviewer *reflectskill.Reviewer
	cancel   context.CancelFunc
}

func newServices() services {
	tasks := task.Default()
	return services{
		Setting:      setting.Default(),
		LLM:          llm.Default(),
		Tool:         tool.Default(),
		Hook:         hook.DefaultEngine(),
		SessionStore: appSessionStore,
		sessionID:    appSessionID,
		Skill:        skill.Default(),
		Subagent:     subagent.Default(),
		Command:      command.Default(),
		Task:         tasks,
		Flow:         flow.NewService(tasks),
		Tracker:      todo.NewStoreWithTasks(tasks),
		Cron:         cron.Default(),
		MCP:          mcp.DefaultManager(),
		Plugin:       plugin.Default(),
		Agent:        agent.Default(),
		Persona:      persona.Default(),
		Reminder:     reminder.NewQueue(),
	}
}
