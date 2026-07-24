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
	"github.com/genai-io/san/internal/todo"
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
	Task     *task.Manager
	Tracker  todo.Service
	Cron     *cron.Scheduler
	MCP      *mcp.Registry
	Plugin   *plugin.Registry
	Agent    *agent.Session
	Persona  *persona.Registry
	Reminder *reminder.Service

	// SelfLearn groups the L1 self-learning state: the live per-session
	// reviewer (nil when no arm is enabled — §3.1 zero-overhead guarantee)
	// and the status-bar Indicator, which is allocated once at services
	// construction and outlives any session's wire/teardown so the render
	// path can always Snapshot() without a nil check.
	SelfLearn SelfLearnServices
}

// SelfLearnServices holds the L1 self-learning state.
type SelfLearnServices struct {
	// session is the live per-session reviewer plus its teardown handles, or
	// nil when no arm is enabled (§3.1 zero overhead: no goroutine, no
	// counters, no extra model calls). Set and cleared as a unit by
	// wireSelfLearn / teardownSelfLearn so the reviewer, its fork-cancel, and
	// its liveness gate can never fall out of sync.
	session *selfLearnSession

	// Indicator drives the four-phase status-bar surface (§"User-visible
	// surface"). Always non-nil; the snapshot reports an idle phase when
	// L1 is off or no review has run yet.
	Indicator *SelfLearnIndicator

	// Recent is the rolling activity log behind the /evolve RECENT zones.
	// Always non-nil; the per-session write observers append to it and it
	// outlives any one session so the recap survives /clear.
	Recent *RecentLearnLog
}

// selfLearnSession bundles the three handles that make up one live L1
// self-learning run for the current conversation session. They are created
// together by wireSelfLearn and dropped together by teardownSelfLearn, so
// holding them behind a single pointer makes the "all present or none"
// invariant structural instead of a prose promise.
type selfLearnSession struct {
	// reviewer is the background L1 trigger, fed by Observe at each turn end.
	reviewer *selflearn.Reviewer

	// cancel cancels the session-scoped context every in-flight fork inherits,
	// so /clear or quit unblocks the fork immediately instead of waiting for
	// its own forkDeadline.
	cancel context.CancelFunc

	// live is flipped to false on teardown so a write observer landing after
	// teardown drops silently instead of mutating UI state for a dead session.
	// The fork goroutine's observers capture this same pointer.
	live *atomic.Bool
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
		Tracker:   todo.Default(),
		Cron:      cron.Default(),
		MCP:       mcp.DefaultRegistry(),
		Plugin:    plugin.Default(),
		Agent:     agent.Default(),
		Persona:   persona.Default(),
		Reminder:  reminder.NewService(),
		SelfLearn: SelfLearnServices{Indicator: NewSelfLearnIndicator(), Recent: NewRecentLearnLog()},
	}
}
