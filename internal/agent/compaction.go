package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/filecache"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/reminder"
	"github.com/genai-io/san/internal/session"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
)

// ErrNothingToCompact avoids summarizing a chain already below the native floor.
var ErrNothingToCompact = errors.New("the conversation is already too short to shorten")

// CompactRequest snapshots manual summarization input before asynchronous work.
// Messages must already be adapted to the selected provider's capabilities.
type CompactRequest struct {
	Ctx          context.Context
	Client       *llm.Client
	Messages     []core.Message
	SummaryFocus string
	HookEngine   hook.Handler
	Trigger      string
}

type CompactResult struct {
	Summary       string
	OriginalCount int
	Trigger       string
	Err           error
}

// Run preserves PreCompact's advisory context, including on a hook block or error.
// The caller's context owns both hook execution and the model request.
func (r CompactRequest) Run() CompactResult {
	focus := r.SummaryFocus
	if r.HookEngine != nil {
		outcome := r.HookEngine.Execute(r.Ctx, hook.PreCompact, hook.HookInput{
			Trigger: r.Trigger, CustomInstructions: r.SummaryFocus,
		})
		if outcome.AdditionalContext != "" {
			if focus != "" {
				focus += "\n"
			}
			focus += outcome.AdditionalContext
		}
	}
	summary, count, err := CompactConversation(r.Ctx, r.Client, r.Messages, focus)
	return CompactResult{Summary: summary, OriginalCount: count, Trigger: r.Trigger, Err: err}
}

func CompactConversation(ctx context.Context, c *llm.Client, msgs []core.Message, focus string) (summary string, count int, err error) {
	count = len(msgs)
	if count < core.MinMessagesToCompact {
		return "", count, ErrNothingToCompact
	}
	conversationText := core.BuildCompactionText(msgs)
	if focus != "" {
		conversationText += fmt.Sprintf("\n\n**Important**: Focus the summary on: %s", focus)
	}
	response, err := c.Complete(ctx, system.CompactPrompt(), []core.Message{core.UserMessage(conversationText, nil)}, nil, core.CompactMaxTokens)
	if err != nil {
		return "", count, fmt.Errorf("failed to generate summary: %w", err)
	}
	summary = strings.TrimSpace(response.Content.Text())
	if summary == "" {
		return "", count, fmt.Errorf("compaction produced empty summary")
	}
	return core.WithRecentRequests(summary, msgs, c.ContextBudget()), count, nil
}

// RecordManualCompaction persists the native boundary for a conversation with no
// running agent. The caller replaces its chain only after this succeeds.
func RecordManualCompaction(opened *session.Session, result CompactResult) (core.Message, core.Compacted, error) {
	summary := core.UserMessage(core.FormatCompactSummary(result.Summary), nil)
	summary.ID = core.NewMessageID()
	info := core.Compacted{Summary: result.Summary, OriginalCount: result.OriginalCount, SummaryMessageID: summary.ID, Trigger: "manual"}
	opened.RecordEvent(sdkagent.MessageAdded{Message: summary})
	opened.RecordEvent(info)
	return summary, info, opened.Err()
}

// PostCompact refreshes context in native order. refreshMemory updates the
// caller's instruction cache; the return value is only a presentation count.
func PostCompact(trigger string, hooks hook.Handler, reminders *reminder.Reminder, files *filecache.Cache, refreshMemory func()) int {
	if trigger == "" {
		trigger = "auto"
	}
	if hooks != nil {
		hooks.ExecuteAsync(hook.PostCompact, hook.HookInput{Trigger: trigger})
	}
	refreshMemory()
	reminders.ResetConversation()
	if trigger == "manual" && files != nil {
		if restored, _ := files.RestoreRecent(); len(restored) > 0 {
			reminders.Enqueue(filecache.FormatRestoredFiles(restored))
			return len(restored)
		}
	}
	return 0
}
