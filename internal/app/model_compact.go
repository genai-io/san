// Conversation compaction: assembling a compact request from current
// messages, handling the agent's auto-compact event, and applying a manual
// /compact result. Both paths flush remaining scrollback, clear the live
// conversation, and reseed it with the compact summary so the next user
// turn restarts from a fresh, shorter history.
package app

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/filecache"
	"github.com/genai-io/san/internal/hook"
)

func (m *model) BuildCompactRequest(focus, trigger string) conv.CompactRequest {
	var hookEngine *hook.Engine
	if m.services.Hook != nil {
		hookEngine = m.services.Hook
	}
	return conv.CompactRequest{
		Ctx:        context.Background(),
		Client:     m.buildLLMClient(),
		Messages:   m.conv.ConvertToProvider(),
		Focus:      focus,
		HookEngine: hookEngine,
		Trigger:    trigger,
	}
}

// OnCompacted handles the CompactEvent emitted by the agent for BOTH
// auto-compaction and manual /compact. By the time it runs the agent has
// already replaced its in-memory chain with the summary and recorded the
// compaction boundary — here we mirror that in the UI and refresh
// session-level reminders. The agent is NOT torn down, so the system prompt
// and tools are reused from cache (no rebuild).
func (m *model) OnCompacted(info core.CompactInfo) tea.Cmd {
	scrollbackCmds := m.commitAllMessages()

	m.conv.Clear()
	m.env.ResetContextDisplay()
	token := m.userInput.Provider.SetStatusMessage("compacted")
	// The summary is injected as a user message — the model reads it (Content),
	// and it persists + seeds resume — but the UI renders it as a single system
	// notice (see RenderMessageAt: IsCompactSummary), so the transcript shows
	// one clean line instead of the raw summary markdown or a "❭" user turn.
	m.conv.Append(core.ChatMessage{
		Role:           core.RoleUser,
		Content:        core.FormatCompactSummary(info.Summary),
		DisplayContent: fmt.Sprintf("≡ Conversation compacted — %d messages summarized (scroll up for history)", info.OriginalCount),
	})

	trigger := info.Trigger
	if trigger == "" {
		trigger = "auto"
	}
	// Manual /compact shows the SESSION SUMMARY box; complete it here so its
	// count matches the boundary line (both info.OriginalCount). Auto-compaction
	// stays silent — just the boundary line.
	if trigger == "manual" {
		m.conv.Compact.Complete(fmt.Sprintf("Condensed %d earlier messages.", info.OriginalCount), false)
	}
	if m.services.Hook != nil {
		m.services.Hook.ExecuteAsync(hook.PostCompact, hook.HookInput{Trigger: trigger})
	}

	// Compaction summarized away the system-reminder content that rode on the
	// old user messages. Re-read memory from disk (a provider renders from the
	// cached instructions, so an edited memory file would otherwise re-inject
	// stale content), drop now-irrelevant one-time notices, and re-emit the
	// providers so skills/memory reattach to the next user turn.
	if m.services.Reminder != nil {
		m.refreshMemoryContext(m.env.CWD, "post_compact")
		m.services.Reminder.DiscardPendingNotices()
		m.services.Reminder.RequeueSystemReminders()

		// Manual /compact restores recently-accessed files as a one-time notice
		// so they ride on the next user turn. Enqueued AFTER DiscardPendingNotices
		// so it survives. Auto-compaction happens mid-task and skips this.
		if trigger == "manual" && m.env.FileCache != nil {
			if restored, _ := m.env.FileCache.RestoreRecent(); len(restored) > 0 {
				m.services.Reminder.Enqueue(filecache.FormatRestoredFiles(restored))
				m.conv.AddNotice(fmt.Sprintf("Restored %d recently accessed file(s) for context.", len(restored)))
			}
		}
	}

	scrollPart := tea.Sequence(append(scrollbackCmds, tea.ClearScreen)...)
	return tea.Batch(scrollPart, m.ContinueOutbox(), kit.StatusTimer(3*time.Second, token))
}

// OnCompactResult applies a manual /compact result. It does not reset the UI
// itself: it asks the running agent to compact in place (which records the
// summary + boundary and emits CompactEvent), and that event drives
// OnCompacted — the same path auto-compaction takes, with no agent rebuild.
// Only when there is no active agent does it drive OnCompacted directly so the
// next session start still seeds the summary.
func (m *model) OnCompactResult(msg conv.CompactResultMsg) tea.Cmd {
	if msg.Error != nil {
		m.conv.Compact.Complete(fmt.Sprintf("Compaction could not be completed: %v", msg.Error), true)
		return tea.Batch(m.CommitMessages()...)
	}

	// Don't complete the SESSION SUMMARY box here: the count is finalized in
	// OnCompacted from the agent's authoritative message count, so the box and
	// the boundary line never disagree. The spinner stays until the agent's
	// CompactEvent arrives.
	if m.services.Agent.Compact(msg.Summary) {
		return tea.Batch(m.CommitMessages()...)
	}
	return m.OnCompacted(core.CompactInfo{
		Summary:       msg.Summary,
		OriginalCount: msg.OriginalCount,
		Trigger:       "manual",
	})
}

func (m *model) OnTokenLimitResult(msg kit.TokenLimitResultMsg) tea.Cmd {
	m.userInput.Provider.FetchingLimits = false
	var content string
	if msg.Error != nil {
		content = "Error: " + msg.Error.Error()
	} else {
		content = msg.Result
	}
	m.conv.AddNotice(content)
	return tea.Batch(m.CommitMessages()...)
}
