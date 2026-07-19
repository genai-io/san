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

// OnAgentMessage observes the agent's MessageEvent echoes. Most paths append the
// user message to m.conv at the call site, so their echo has nothing to do here —
// appending again would double-display. The exception is a queued message
// released mid-turn by DrainQueuedAtStep: it is sent to the inbox WITHOUT a
// call-site append, so it is displayed here, when the agent has actually ingested
// it — which lands its "❭" line right before the step that addresses it. Matched
// by content in FIFO order against stepDrainPending.
func (m *model) OnAgentMessage(msg core.Message) tea.Cmd {
	if len(m.stepDrainPending) > 0 && m.stepDrainPending[0] == msg.Content {
		m.stepDrainPending = m.stepDrainPending[1:]
		m.conv.Append(core.ChatMessage{Role: core.RoleUser, Content: msg.Content, Images: msg.Images})
		return tea.Batch(m.CommitMessages()...)
	}
	return nil
}

// DrainQueuedAtStep releases one queued user message to the running agent at a
// step boundary (a PostTool, where the turn continues). The agent's drainInbox
// waits briefly for it (m.pendingInput), takes it into the next step, and the
// LLM's next response addresses it — so the message stays editable in the queue
// right up to this release. One release per step (drainedThisStep); the head
// item is left in place while under edit. Display happens on the ingest echo.
func (m *model) DrainQueuedAtStep() tea.Cmd {
	if m.drainedThisStep || !m.services.Agent.Active() || m.userInput.Queue.SelectIdx == 0 {
		return nil
	}
	item, ok := m.userInput.Queue.Dequeue()
	if !ok {
		return nil
	}
	m.drainedThisStep = true
	if m.imagesBlockedForModel(item.Images) {
		// Return it rather than dropping it; the notice tells the user why.
		m.userInput.ReturnToTextarea(item.Content, item.Images)
		return tea.Batch(m.CommitMessages()...)
	}
	m.stepDrainPending = append(m.stepDrainPending, item.Content)
	svc, content, images := m.services.Agent, item.Content, item.Images
	return func() tea.Msg {
		svc.Send(content, images)
		return nil
	}
}

// syncPendingInput mirrors "the queue has a message ready for the agent" into
// m.pendingInput, so the agent's drainInbox waits briefly for it at a step
// boundary. A head item under edit is not ready (DrainQueuedAtStep skips it), so
// the agent must not wait on it. Called after every event (see Update).
func (m *model) syncPendingInput() {
	if m.pendingInput == nil {
		return
	}
	m.pendingInput.Store(m.userInput.Queue.Len() > 0 && m.userInput.Queue.SelectIdx != 0)
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
