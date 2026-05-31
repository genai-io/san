package app

import (
	"context"

	"github.com/genai-io/gen-code/internal/agent"
	"github.com/genai-io/gen-code/internal/command"
	"github.com/genai-io/gen-code/internal/cron"
	"github.com/genai-io/gen-code/internal/hook"
	"github.com/genai-io/gen-code/internal/identity"
	"github.com/genai-io/gen-code/internal/llm"
	"github.com/genai-io/gen-code/internal/mcp"
	"github.com/genai-io/gen-code/internal/plugin"
	"github.com/genai-io/gen-code/internal/reminder"
	"github.com/genai-io/gen-code/internal/selflearn"
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
	Setting  *setting.Settings
	LLM      *llm.ClientFactory
	Tool     *tool.Registry
	Hook     *hook.Engine
	Session  *session.Setup
	Skill    *skill.Registry
	Subagent *subagent.Registry
	Command  *command.Registry
	Task     *task.Tracker
	Tracker  tracker.Service
	Cron     *cron.Scheduler
	MCP      *mcp.Registry
	Plugin   *plugin.Registry
	Agent    *agent.Task
	Identity *identity.Registry
	Reminder *reminder.Service

	// SelfLearn is the L1 background reviewer. It is non-nil only when the
	// selfLearn settings have at least one arm enabled at session start
	// (see notes/active/l1-background-review.md §3.1 / §9). Nil ⇒ zero
	// overhead: no goroutine, no counters, no extra model calls.
	SelfLearn *selflearn.Reviewer

	// SelfLearnCancel cancels the session-scoped context every in-flight
	// reviewer fork inherits. Called from StopAgentSession so a /clear or
	// quit unblocks the fork immediately instead of waiting for the
	// 5-minute deadline; never nil while SelfLearn is non-nil.
	SelfLearnCancel context.CancelFunc

	// SelfLearnUI drives the four-phase status-bar surface from the design's
	// §"User-visible surface". Always non-nil so the render path can take
	// Snapshot() without a nil check; the snapshot reports an idle phase
	// when L1 is off or no review has run yet.
	SelfLearnUI *SelfLearnUIState
}

func newServices() services {
	return services{
		Setting:  setting.Default(),
		LLM:      llm.Default(),
		Tool:     tool.Default(),
		Hook:     hook.DefaultEngine(),
		Session:  session.Default(),
		Skill:    skill.Default(),
		Subagent: subagent.Default(),
		Command:  command.Default(),
		Task:     task.Default(),
		Tracker:  tracker.Default(),
		Cron:     cron.Default(),
		MCP:      mcp.DefaultRegistry(),
		Plugin:   plugin.Default(),
		Agent:       agent.Default(),
		Identity:    identity.Default(),
		Reminder:    reminder.NewService(),
		SelfLearnUI: NewSelfLearnUIState(),
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
	s.MCP = mcp.DefaultRegistry()
	s.Identity = identity.Default()
}
