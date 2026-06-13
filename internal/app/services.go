package app

import (
	"context"
	"sync/atomic"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/command"
	"github.com/genai-io/san/internal/cron"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/mcp"
	"github.com/genai-io/san/internal/persona"
	"github.com/genai-io/san/internal/plugin"
	"github.com/genai-io/san/internal/reminder"
	"github.com/genai-io/san/internal/selflearn"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/skill"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/task/tracker"
	"github.com/genai-io/san/internal/tool"
)

// services holds references to domain service singletons, injected into
// model at construction time. Model methods access services through this
// struct instead of calling Default() package-level accessors directly.
type services struct {
	Setting  *setting.Settings
	LLM      *llm.Conn
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
	Persona  *persona.Registry
	Reminder *reminder.Service

	// SelfLearn groups the L1 self-learning state. Reviewer / Cancel / Live
	// are populated per wiring (zero when no arm is enabled — §3.1 zero-
	// overhead guarantee). Indicator is allocated once at services
	// construction and outlives wiring/teardown so the render path can
	// always Snapshot() without a nil check.
	SelfLearn SelfLearnServices
}

// SelfLearnServices bundles the L1 fields that move together. See
// notes/active/l1-background-review.md §9 step 4.
type SelfLearnServices struct {
	// Reviewer is the background L1 trigger. Non-nil only when an arm is
	// enabled at session start; nil ⇒ zero overhead (no goroutine, no
	// counters, no extra model calls).
	Reviewer *selflearn.Reviewer

	// Cancel cancels the session-scoped context every in-flight fork
	// inherits. Called from StopAgentSession so a /clear or quit unblocks
	// the fork immediately instead of waiting for the forkDeadline; never
	// nil while Reviewer is non-nil.
	Cancel context.CancelFunc

	// Live is flipped to false when the current wiring is torn down so a
	// late write observer drops silently instead of racing on the Reviewer
	// pointer. Allocated per wiring; nil before the first wiring.
	Live *atomic.Bool

	// Indicator drives the four-phase status-bar surface (§"User-visible
	// surface"). Always non-nil; the snapshot reports an idle phase when
	// L1 is off or no review has run yet.
	Indicator *SelfLearnIndicator
}

func newServices() services {
	return services{
		Setting:   setting.Default(),
		LLM:       llm.Default(),
		Tool:      tool.Default(),
		Hook:      hook.DefaultEngine(),
		Session:   session.Default(),
		Skill:     skill.Default(),
		Subagent:  subagent.Default(),
		Command:   command.Default(),
		Task:      task.Default(),
		Tracker:   tracker.Default(),
		Cron:      cron.Default(),
		MCP:       mcp.DefaultRegistry(),
		Plugin:    plugin.Default(),
		Agent:     agent.Default(),
		Persona:   persona.Default(),
		Reminder:  reminder.NewService(),
		SelfLearn: SelfLearnServices{Indicator: NewSelfLearnIndicator()},
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
	s.Persona = persona.Default()
}
