// conv.Runtime implementation: callbacks the agent's outbox event pump calls
// on the main bubbletea goroutine — turn start, token accounting, tool
// results, turn end, and stop. The actual side effects (committing
// scrollback, draining queues, firing hooks) live in adjacent model_*
// files; this file is the thin wire between agent events and those
// effects.
package app

import (
	"context"
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/tool"
)

func (m *model) OnTokenUsage(resp *core.InferResponse) {
	if resp == nil {
		return
	}
	// PostInfer starts a new step: re-arm the one-per-step queue release that
	// this step's PostTool events will use (see DrainQueuedAtStep).
	m.drainedThisStep = false

	if m.userInput.Provider.StatusMessage == "compacted" {
		m.userInput.Provider.StatusMessage = ""
	}

	// Bottom-right context usage reflects the latest infer call, not a
	// lifetime sum across the whole session. Use the full prompt size
	// (fresh + cache read + cache creation) so the ctx readout matches real
	// context-window occupancy rather than just the uncached delta.
	m.env.InputTokens = resp.TotalInputTokens()
	m.env.OutputTokens = resp.OutputTokens

	if m.env.CurrentModel != nil {
		usage := llm.Usage{
			InputTokens:              resp.InputTokens,
			OutputTokens:             resp.OutputTokens,
			CacheCreationInputTokens: resp.CacheCreationInputTokens,
			CacheReadInputTokens:     resp.CacheReadInputTokens,
		}
		if cost, ok := llm.EstimateCost(m.env.CurrentModel.Provider, m.env.CurrentModel.ModelID, usage); ok {
			m.env.ConversationCost = m.env.ConversationCost.Add(cost)
		}
	}
}

func (m *model) HasRunningTasks() bool { return m.services.Tracker.HasInProgress() }

// OnAgentMessage observes the agent's MessageEvent echoes only. Every path that
// hands a user message to the agent — idle submit, queue release
// (releaseQueuedMessage), cron prompt, async hook — appends it to m.conv at the
// call site, so the echo has nothing to do here: appending again would
// double-display.
func (m *model) OnAgentMessage(core.Message) tea.Cmd {
	return nil
}

// DrainQueuedAtStep releases one queued user message to the running agent at a
// step boundary (a PostTool, where the turn continues), so a following LLM
// response addresses it — the message stays editable in the queue right up to
// this release. One release per step (drainedThisStep). The shared release
// mechanics live in releaseQueuedMessage, shared with the turn-boundary drain.
func (m *model) DrainQueuedAtStep() tea.Cmd {
	if m.drainedThisStep || !m.services.Agent.Active() {
		return nil
	}
	cmd, released := m.releaseQueuedMessage()
	if released {
		m.drainedThisStep = true
	}
	return cmd
}

func (m *model) OnToolResult(tr core.ToolResult) *core.ToolResult {
	// Track skill usage for the self-learning trigger: any Skill tool call this
	// turn (even a failing one — a broken skill is a prime refine/retire
	// candidate) flips the turn onto the update/delete review path.
	if tr.ToolName == tool.ToolSkill {
		m.skillUsedThisTurn = true
	}
	// The Evolve tool is the model-decided self-learning trigger: its call
	// this turn queues a review at turn end.
	if tr.ToolName == tool.ToolEvolve && !tr.IsError {
		m.evolveRequestedThisTurn = true
	}

	sideEffect := m.services.Tool.PopSideEffect(tr.ToolCallID)
	details := m.services.Tool.PopResultDetails(tr.ToolCallID)
	if sideEffect != nil {
		m.applyToolSideEffects(tr.ToolName, sideEffect)
	}
	m.firePostToolHook(tr, sideEffect)

	result := &core.ToolResult{
		ToolCallID: tr.ToolCallID,
		ToolName:   tr.ToolName,
		Content:    tr.Content,
		IsError:    tr.IsError,
		Details:    details,
	}
	m.persistOverflow(result)
	return result
}

func (m *model) OnTurnEnd(result core.Result) tea.Cmd {
	if m.services.Tracker.AllDone() {
		m.services.Tracker.Reset()
	}
	m.services.Agent.SetPluginRoot("")
	// Forward to L1 self-learning with whether this turn used a skill and
	// whether the model called Evolve (the model-decided trigger). No-op when
	// disabled; the reviewer gates on StopEndTurn internally so cancelled /
	// interrupted turns are skipped. Clear both flags for the next turn either
	// way.
	if s := m.services.SelfLearn.session; s != nil {
		s.reviewer.Observe(result, m.skillUsedThisTurn, m.evolveRequestedThisTurn)
	}
	m.skillUsedThisTurn = false
	m.evolveRequestedThisTurn = false
	log.QueueLog("OnTurnEnd: starting queueLen=%d", m.userInput.Queue.Len())
	commitCmds := m.CommitMessages()

	if cmd, found := m.drainTurnQueues(); found {
		log.QueueLog("OnTurnEnd: drained queued message, skipping hooks")
		if cmd != nil {
			commitCmds = append(commitCmds, cmd)
		}
		commitCmds = append(commitCmds, m.ContinueOutbox())
		return tea.Batch(commitCmds...)
	}

	// User-initiated cancel surfaces here as a Result with StopCancelled now
	// that ThinkAct returns a phantom Result on context.Canceled. Stop /
	// idle-notification hooks would otherwise fire on every Esc — confusing
	// for the user and for hooks that template result.Content (which is
	// empty for a cancelled turn). We still persist so the [Interrupted]
	// marker and cancelled tool_result rows survive a crash/quit, and
	// re-arm prompt suggestions for the now-idle textarea.
	if result.StopReason == core.StopCancelled {
		log.QueueLog("OnTurnEnd: turn was cancelled, skipping idle hooks")
		if cmd := m.persistAfterTurn(); cmd != nil {
			commitCmds = append(commitCmds, cmd)
		}
		if cmd := m.startPromptSuggestion(); cmd != nil {
			commitCmds = append(commitCmds, cmd)
		}
		commitCmds = append(commitCmds, m.ContinueOutbox())
		return tea.Batch(commitCmds...)
	}

	// Autopilot TurnEnd steer (#5): let the copilot decide whether to keep the
	// session going toward the mission. When it wants to, the idle hooks are
	// deferred until handleAutopilotDecision (which fires them if it stops).
	if cmd := m.autopilotContinueCmd(result); cmd != nil {
		log.QueueLog("OnTurnEnd: autopilot deciding whether to continue")
		commitCmds = append(commitCmds, cmd, m.ContinueOutbox())
		return tea.Batch(commitCmds...)
	}

	log.QueueLog("OnTurnEnd: firing idle hooks async")
	commitCmds = append(commitCmds, m.fireIdleHooksCmd(result), m.ContinueOutbox())
	return tea.Batch(commitCmds...)
}

func (m *model) OnAgentStop(err error) tea.Cmd {
	// /clear and manual stop cancel the active agent context; that is expected
	// shutdown, not an agent failure the user needs to see.
	if err != nil && !errors.Is(err, context.Canceled) {
		m.conv.AddNotice(fmt.Sprintf("Agent error: %v", err))
		m.fireStopFailureHook(core.LastAssistantChatContent(m.conv.Messages), err)
	}
	m.conv.AgentToUI.DrainPendingQuestions()
	m.conv.Modal.Question.Hide()
	commitCmds := m.CommitMessages()
	m.StopAgentSession()
	return tea.Batch(commitCmds...)
}
