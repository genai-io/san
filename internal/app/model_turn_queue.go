// Turn-boundary inbox drain and prompt injection. After every agent turn
// ends we drain (in priority order) queued user messages, cron-fired
// prompts, async-hook continuations, and the main agentEventHub buffer. Each
// drained item is converted to a notice + optional re-send to the agent.
// Also handles the Stop hook result that gates session persistence.
package app

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"go.uber.org/zap"

	"github.com/genai-io/san/internal/app/hub"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/app/trigger"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/log"
)

const maxEventsPerDrain = 8

func (m *model) handleStopHookResult(msg stopHookResultMsg) tea.Cmd {
	if msg.Blocked {
		log.QueueLog("handleStopHookResult: hooks BLOCKED reason=%q", msg.Reason)
		blockMsg := "Stop hook blocked: " + msg.Reason
		m.conv.Append(core.ChatMessage{Role: core.RoleUser, Content: blockMsg})
		return m.sendToAgent(blockMsg, nil)
	}
	log.QueueLog("handleStopHookResult: hooks done, persisting")
	var cmds []tea.Cmd
	if m.services.Session.ID() != "" {
		cmds = append(cmds, m.persistSessionCmd())
	} else {
		if err := m.PersistSession(); err != nil {
			log.Logger().Warn("failed to save session", zap.Error(err))
		}
	}
	if cmd := input.StartPromptSuggestion(m.promptSuggestionDeps()); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if msg.Result.StopReason != "" && msg.Result.StopReason != core.StopEndTurn {
		m.conv.AddNotice(fmt.Sprintf("Agent stopped: %s", msg.Result.StopReason))
		if msg.Result.StopDetail != "" {
			m.conv.AddNotice(msg.Result.StopDetail)
		}
	}
	if len(cmds) > 0 {
		return tea.Batch(cmds...)
	}
	return nil
}

func (m *model) drainTurnQueues() (tea.Cmd, bool) {
	// Drain ONE user message per call so each gets its own agent response.
	// The agent's inner loop also drains one inbox message at a time,
	// producing one TurnEvent per queued message.
	if item, ok := m.userInput.Queue.Dequeue(); ok {
		log.QueueLog("drainTurnQueues: dequeued %q remaining=%d", truncate(item.Content, 60), m.userInput.Queue.Len())
		m.conv.Append(core.ChatMessage{Role: core.RoleUser, Content: item.Content, Images: item.Images})
		m.services.Agent.Send(item.Content, item.Images)
		return nil, true
	}

	if len(m.systemInput.CronQueue) > 0 {
		prompt := m.systemInput.CronQueue[0]
		m.systemInput.CronQueue = m.systemInput.CronQueue[1:]
		return m.injectCronPrompt(prompt), true
	}

	if m.systemInput.AsyncHookQueue != nil {
		if item, ok := m.systemInput.AsyncHookQueue.Pop(); ok {
			return m.injectAsyncHookContinuation(item), true
		}
	}

	if len(m.pendingMainEvents) > 0 {
		events := m.pendingMainEvents
		m.pendingMainEvents = nil
		// Listener may have just re-armed during OnTurnEnd processing;
		// catch any chan events that landed in that window too.
		if extra := drainEvents(m.mainEvents, maxEventsPerDrain-len(events)); len(extra) > 0 {
			events = append(events, extra...)
		}
		return m.injectHubEvents(events), true
	}

	return nil, false
}

// injectNotification surfaces a background event (task completion, agent
// message) into the live conversation. Notice + content come from hub.Merge
// over a batch of hub.Events.
func (m *model) injectNotification(msg hub.Message) tea.Cmd {
	if msg.Notice != "" {
		m.conv.AddNotice(msg.Notice)
	}
	if msg.Content == "" {
		return tea.Batch(m.CommitMessages()...)
	}
	return m.SubmitToAgent(msg.Content, nil)
}

func drainEvents(ch <-chan hub.Event, max int) []hub.Event {
	var out []hub.Event
	for range max {
		select {
		case e := <-ch:
			out = append(out, e)
		default:
			return out
		}
	}
	return out
}

// mainEventMsg wraps a hub.Event for delivery to the Update loop.
// Counterpart to AgentOutboxMsg for the agent outbox chan.
type mainEventMsg struct{ event hub.Event }

// awaitMainEvent blocks until one event arrives on the chan, then
// yields a mainEventMsg. onMainEvent re-arms after handling.
func awaitMainEvent(ch <-chan hub.Event) tea.Cmd {
	return func() tea.Msg {
		return mainEventMsg{event: <-ch}
	}
}

// onMainEvent injects the event immediately when idle, or queues it
// in pendingMainEvents for drainTurnQueues at the next turn boundary
// (mid-stream). Re-arming is unconditional: after the read the chan
// is empty, so the next firing waits for the next publish.
func (m *model) onMainEvent(ev hub.Event) tea.Cmd {
	next := awaitMainEvent(m.mainEvents)
	// Selflearn tick-start events are internal spinner wake-ups, not
	// user-visible notices. TryStartTicker keeps back-to-back reviews
	// from stacking parallel tick chains.
	if ev.Type == eventSelfLearnReviewStarted {
		if m.services.SelfLearn.Indicator != nil && m.services.SelfLearn.Indicator.TryStartTicker() {
			return tea.Batch(scheduleSelflearnTick(), next)
		}
		return next
	}
	if m.conv.Stream.Active {
		m.pendingMainEvents = append(m.pendingMainEvents, ev)
		return next
	}
	return tea.Batch(m.injectHubEvents([]hub.Event{ev}), next)
}

func (m *model) injectHubEvents(events []hub.Event) tea.Cmd {
	return m.injectNotification(hub.Merge(eventsToMessages(events)))
}

func eventsToMessages(events []hub.Event) []hub.Message {
	msgs := make([]hub.Message, len(events))
	for i, e := range events {
		msgs[i] = hub.Message{Notice: e.Subject, Content: e.Data}
	}
	return msgs
}

// injectCronPrompt fires a scheduled cron prompt as if the user had just
// typed it. The notice + user message show what triggered; SubmitToAgent
// handles provider/agent state.
func (m *model) injectCronPrompt(prompt string) tea.Cmd {
	m.conv.AddNotice("Scheduled task fired")
	m.conv.Append(core.ChatMessage{Role: core.RoleUser, Content: prompt})
	return m.SubmitToAgent(prompt, nil)
}

// injectAsyncHookContinuation surfaces an async hook's follow-up: the hook
// pushed one or more context lines + a continuation prompt; we display the
// context as user messages and submit the continuation to the agent.
func (m *model) injectAsyncHookContinuation(item trigger.AsyncHookRewake) tea.Cmd {
	if item.Notice != "" {
		m.conv.AddNotice(item.Notice)
	}
	if len(item.Context) == 0 {
		return tea.Batch(m.CommitMessages()...)
	}
	for _, ctx := range item.Context {
		m.conv.Append(core.ChatMessage{Role: core.RoleUser, Content: ctx})
	}
	return m.SubmitToAgent(item.ContinuationPrompt, nil)
}
