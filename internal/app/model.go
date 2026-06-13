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
//	model_deps.go          deps builders for sub-features
//	model_actions.go       persona switch (hot-patch prompt parts + skills)
package app

import (
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/hub"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/app/trigger"
)

const defaultWidth = 80

type model struct {
	// ── Sub-models (one per event source / concern) ─────────────
	userInput         input.Model    // Source 1: user keyboard input
	agentEventHub     *hub.Hub       // Source 2: inter-agent event routing (pure pub/sub)
	mainEvents        chan hub.Event // hub-side delivery chan; awaitMainEvent reads it
	pendingMainEvents []hub.Event    // events that arrived mid-stream, drained at OnTurnEnd
	systemInput       trigger.Model  // Source 3: system events (cron/hooks/watcher)
	conv              conv.Model     // Agent Outbox: conversation + output rendering
	env               env            // Shared app state: provider, session, permission, plan, config
	services          services       // Domain service singletons, injected at construction
}

var (
	_ conv.Runtime          = (*model)(nil)
	_ input.ApprovalRuntime = (*model)(nil)
)

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
