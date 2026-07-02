// Root bubbletea model. Holds the four event sources (user input, system
// triggers, agent outbox, inter-agent event hub), the env state, and the
// services struct. Init batches the initial commands (cursor blink, MCP
// autoconnect, cron + async-hook tickers, optional initial prompt).
//
// All the model's *behavior* lives in sibling files:
//
//	model_lifecycle.go     construction + run-option application + task
//	                       lifecycle wiring + SessionEnd shutdown
//	model_session.go       session save/load + per-session task storage
//	model_scrollback.go    rendering committed messages to terminal output
//	model_agent_events.go  conv.Runtime callbacks invoked by the agent
//	                       outbox pump
//	model_compact.go       conversation compaction (auto + /compact)
//	model_tool_effects.go  side effects from tool calls (cwd, files, agent
//	                       launches, oversized-output persistence)
//	model_workspace.go     cwd/file change reactions + FileWatcher setup
//	model_turn_queue.go    inbox drain + prompt injection at turn end +
//	                       stop-hook gate before persistence
//	model_subfeatures.go   deps builders for sub-features
//	model_actions.go       persona switch (hot-patch prompt parts + skills)
package app

import (
	"sync"
	"sync/atomic"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/desktop"
	"github.com/genai-io/san/internal/app/hub"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/app/trigger"
)

const defaultWidth = 80

type model struct {
	// ── Sub-models (one per event source / concern) ─────────────
	userInput         input.Model     // Source 1: user keyboard input
	agentEventHub     *hub.Hub        // Source 2: inter-agent event routing (pure pub/sub)
	mainEvents        chan hub.Event  // hub-side delivery chan; awaitMainEvent reads it
	pendingMainEvents []hub.Event     // events that arrived mid-stream, drained at OnTurnEnd
	systemInput       trigger.Model   // Source 3: system events (cron/hooks/watcher)
	conv              conv.Model      // Agent Outbox: conversation + output rendering
	desktop           desktop.Manager // Alt-screen "desktop" surface (opt-in via ctrl+g)
	env               env             // Shared app state: provider, session, permission, plan, config
	services          services        // Domain service singletons, injected at construction

	// Desktop-surface scratch state (see desktop_surface.go): a single-chain
	// guard for the animation tick, plus a width-keyed cache of the markdown
	// renderer so streaming into the desktop's conversation window doesn't
	// rebuild glamour every frame.
	desktopTicking bool
	desktopMD      *conv.MDRenderer
	desktopMDWidth int

	// welcomePending marks the startup splash as not yet frozen into scrollback.
	// While set, the splash renders live above the input (visible from launch
	// and tracking the model the user picks); the first scrollback commit then
	// freezes that same banner — with the now-selected model — and clears this.
	// Set in Run for fresh sessions. See view.go (liveWelcome) and
	// model_scrollback.go (takeWelcomeBanner).
	welcomePending bool

	// reviewerApprovals / reviewerEscalations count auto-review outcomes this
	// session for the status bar: gray-zone tool calls the judge auto-approved
	// vs. handed back to the user. Pointers so value-receiver copies of the
	// model share one counter across the agent and UI goroutines.
	reviewerApprovals   *atomic.Int64
	reviewerEscalations *atomic.Int64

	// pendingDecisions maps a tool call ID to the auto-review judge's decision,
	// so the renderer can draw it inline under that tool call. Written on the
	// agent goroutine (in PermissionReview, before the tool runs) and read on
	// the UI goroutine at render time — a sync.Map, shared by pointer across
	// value-receiver copies of the model.
	pendingDecisions *sync.Map // tool call ID → core.ReviewDecision

	// autopilot holds the live autopilot snapshot (judge + resolved config),
	// hot-swapped when the /autopilot panel saves so model/prompt/steer changes
	// take effect without an agent restart. m.env.AutoPilot is UI-goroutine-owned
	// and can be reassigned by a mid-turn Save, so the agent side never reads it
	// directly — it Loads this snapshot instead (single-word swap is race-free).
	autopilot *atomic.Pointer[autopilotRuntime]

	// autopilotContinuations counts TurnEnd auto-continuations since the last
	// human turn (reset in dispatchSubmission); autopilotContinuing tags the
	// in-flight submit as copilot-driven so that reset skips it.
	autopilotContinuations int
	autopilotContinuing    bool

	// autopilotDeciding is true while a turn-end/kick decision is in flight, so
	// the mode indicator shows "thinking…" instead of a transcript notice.
	autopilotDeciding bool

	// Streaming blocks render their markdown off the UI goroutine so a completed
	// block never stalls repaint. See flushState and model_scrollback.go.
	flush flushState
}

var _ conv.Runtime = (*model)(nil)

func (m *model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		textarea.Blink,
		m.userInput.MCP.Selector.AutoConnect(),
		trigger.TriggerCronTickNow(),
		trigger.StartCronTicker(),
		trigger.StartAsyncHookTicker(),
		awaitMainEvent(m.mainEvents),
	}
	if m.env.InitialPrompt != "" {
		prompt := m.env.InitialPrompt
		cmds = append(cmds, func() tea.Msg { return initialPromptMsg(prompt) })
	}
	return tea.Batch(cmds...)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
