// Conversation compaction: assembling a compact request from current
// messages, handling the agent's auto-compact event, and applying a manual
// /compact result. Both paths flush remaining scrollback, clear the live
// conversation, and reseed it with the compact summary so the next user
// turn restarts from a fresh, shorter history.
package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/session"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
)

// pendingCompaction is owned only by the UI goroutine. The command captures
// its context and immutable request input, never the model or its live owner.
type pendingCompaction struct {
	ctx       context.Context
	cancel    context.CancelFunc
	session   *session.LegacySession
	sessionID string
	outbox    <-chan core.Event
}

func (m *model) cancelManualCompaction() {
	if p := m.pendingCompaction; p != nil {
		p.cancel()
		m.pendingCompaction = nil
		m.conv.Compact.Clear()
	}
}

func (m *model) BuildCompactRequest(focus, trigger string) agent.CompactRequest {
	client := m.buildLLMClient()
	messages := client.RequestMessages(m.services.Agent.Messages())
	ctx := context.Background()
	// Rejected short requests never acquire a lifetime or disturb the UI.
	if len(messages) >= core.MinMessagesToCompact {
		m.cancelManualCompaction()
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		m.pendingCompaction = &pendingCompaction{ctx: ctx, cancel: cancel,
			session: m.services.Session, sessionID: m.sessionID(), outbox: m.AgentOutbox()}
	}
	return agent.CompactRequest{
		Ctx:    ctx,
		Client: client,
		// Summarization runs on the active model, so the chain has to clear the
		// same bar as an agent turn: a text-only provider rejects image parts,
		// and the conversation keeps images even when the model can't read them.
		Messages:     messages,
		SummaryFocus: focus,
		HookEngine:   m.services.Hook,
		Trigger:      trigger,
	}
}

// OnCompactEnd clears automatic progress on both success and failure. A failed
// summarization can end the turn without another inference to clear the status.
func (m *model) OnCompactEnd(sdkagent.CompactionEnd) tea.Cmd {
	m.conv.Compact.Clear()
	return nil
}

func (m *model) OnCompactStart(count int) tea.Cmd {
	m.conv.Stream.Stop()
	m.conv.Compact.Active = true
	m.conv.Compact.AutoContinue = true
	m.conv.Compact.StartedAt = time.Now()
	m.conv.Compact.Phase = conv.PhaseSummarizing
	m.conv.Compact.Count = count
	return m.conv.Spinner.Tick
}

// OnCompacted handles the CompactEvent emitted by the agent for BOTH
// auto-compaction and manual /compact. By the time it runs the agent has
// already replaced its in-memory chain with the summary and recorded the
// compaction boundary — here we mirror that in the UI and refresh
// session-level reminders. The agent is NOT torn down, so the system prompt
// and tools are reused from cache (no rebuild).
func (m *model) OnCompacted(info core.Compacted) tea.Cmd {
	scrollbackCmds := m.commitAllMessages()

	m.conv.Clear()
	m.env.ResetContextDisplay()
	m.env.Compressions++
	token := m.userInput.Provider.SetStatusMessage("compacted")
	// The summary is injected as a user message — the model reads it (Content),
	// and it persists + seeds resume — but the UI renders it as a single system
	// notice (see RenderMessageAt: IsCompactSummary), so the transcript shows
	// one clean line instead of the raw summary markdown or a "❭" user turn.
	m.conv.Append(core.ChatMessage{
		ID:             info.SummaryMessageID,
		Role:           core.ChatUser,
		Content:        core.FormatCompactSummary(info.Summary),
		DisplayContent: fmt.Sprintf("≡ Conversation compacted — %s summarized (scroll up for history)", kit.Plural(info.OriginalCount, "message")),
	})

	trigger := info.Trigger
	if trigger == "" {
		trigger = "auto"
	}
	// Manual /compact shows the SESSION SUMMARY box; complete it here so its
	// count matches the boundary line (both info.OriginalCount). Auto-compaction
	// stays silent — just the boundary line — but its OnCompactStart progress
	// line still has to be cleared now that the summary is in.
	if trigger == "manual" {
		m.conv.Compact.Complete(fmt.Sprintf("Condensed %d earlier messages.", info.OriginalCount), false)
	} else {
		m.conv.Compact.Clear()
	}
	if restored := agent.PostCompact(trigger, m.services.Hook, m.services.Reminder, m.env.FileCache, func() {
		m.refreshMemoryContext(m.env.CWD, "post_compact")
	}); restored > 0 {
		m.conv.AddNotice(fmt.Sprintf("Restored %d recently accessed file(s) for context.", restored))
	}

	scrollPart := tea.Sequence(append(scrollbackCmds, tea.ClearScreen)...)
	return tea.Batch(scrollPart, m.ListenForAgentEvents(), kit.StatusTimer(3*time.Second, token), m.sendNextQueuedInputIfIdle())
}

// HandleCompactResult applies a manual /compact result. It does not reset the UI
// itself: it asks the running agent to compact in place (which records the
// summary + boundary and emits CompactEvent), and that event drives
// OnCompacted — the same path auto-compaction takes, with no agent rebuild.
// Only when there is no active agent does it drive OnCompacted directly so the
// next session start still seeds the summary.
func (m *model) HandleCompactResult(msg conv.CompactResultMsg) tea.Cmd {
	p := m.pendingCompaction
	if p == nil || msg.Context != p.ctx || p.ctx.Err() != nil ||
		p.session != m.services.Session || p.sessionID != m.sessionID() || p.outbox != m.AgentOutbox() {
		return nil
	}
	// Release ownership before opening an inactive transcript (which replaces
	// the nil recording owner). Validation precedes even error presentation.
	p.cancel()
	m.pendingCompaction = nil
	if errors.Is(msg.Err, agent.ErrNothingToCompact) {
		// Not a failure: there was nothing to do. Saying so is the point —
		// pressing /compact on a summary used to spend a model call to
		// summarise it again, a little worse each time.
		m.conv.Compact.Complete("Nothing to compact — the conversation is already this short.", false)
		return tea.Batch(append(m.CommitMessages(), m.sendNextQueuedInputIfIdle())...)
	}
	if msg.Err != nil {
		m.conv.Compact.Complete(fmt.Sprintf("Compaction could not be completed: %v", msg.Err), true)
		return tea.Batch(m.CommitMessages()...)
	}

	// Don't complete the SESSION SUMMARY box here: the count is finalized in
	// OnCompacted from the agent's authoritative message count, so the box and
	// the boundary line never disagree. The spinner stays until the agent's
	// CompactEvent arrives.
	if m.services.Agent.Compact(msg.Summary) {
		return tea.Batch(m.CommitMessages()...)
	}
	// A resumed conversation may not have a runtime yet. Record the same
	// native summary and boundary as live compaction before replacing the UI.
	if err := m.ensureSessionStore(); err != nil {
		m.conv.Compact.Complete(fmt.Sprintf("Compaction could not be saved: %v", err), true)
		return tea.Batch(m.CommitMessages()...)
	}
	opened, err := m.openCurrentSession()
	if err != nil {
		m.conv.Compact.Complete(fmt.Sprintf("Compaction could not be saved: %v", err), true)
		return tea.Batch(m.CommitMessages()...)
	}
	summary, info, err := agent.RecordManualCompaction(opened, msg.CompactResult)
	if err != nil {
		m.conv.Compact.Complete(fmt.Sprintf("Compaction could not be saved: %v", err), true)
		return tea.Batch(m.CommitMessages()...)
	}
	m.services.Agent.SetMessages([]core.Message{summary})
	return m.OnCompacted(info)
}
