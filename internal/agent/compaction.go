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
	PreCompact   func(context.Context) hook.HookOutcome
	Trigger      string
	Completion   *session.Completion
	Err          error
}

type CompactResult struct {
	Summary       string
	OriginalCount int
	Trigger       string
	Completion    *session.Completion
	Err           error
}

// Run preserves PreCompact's advisory context, including on a hook block or error.
// The caller's context owns both hook execution and the model request.
func (r CompactRequest) Run() (result CompactResult) {
	result.Trigger, result.Completion = r.Trigger, r.Completion
	defer func() {
		if result.Err != nil {
			result.Err = errors.Join(result.Err, r.Completion.Close())
		}
	}()
	if r.Err != nil {
		result.Err = r.Err
		return result
	}
	if err := r.Ctx.Err(); err != nil {
		result.Err = err
		return result
	}
	focus := r.SummaryFocus
	if r.PreCompact != nil {
		outcome := r.PreCompact(r.Ctx)
		var recording *hook.RecordingError
		if errors.As(outcome.Error, &recording) {
			result.Err = outcome.Error
			return result
		}
		if outcome.AdditionalContext != "" {
			if focus != "" {
				focus += "\n"
			}
			focus += outcome.AdditionalContext
		}
	}
	if err := r.Ctx.Err(); err != nil {
		result.Err = err
		return result
	}
	result.Summary, result.OriginalCount, result.Err = CompactConversation(r.Ctx, r.Client, r.Messages, focus, r.Completion.Admission())
	if result.Err == nil {
		result.Err = r.Ctx.Err()
	}
	return result
}

func CompactConversation(ctx context.Context, c *llm.Client, msgs []core.Message, focus string, admission llm.CompletionAdmission) (summary string, count int, err error) {
	count = len(msgs)
	if count < core.MinMessagesToCompact {
		return "", count, ErrNothingToCompact
	}
	conversationText := core.BuildCompactionText(msgs)
	if focus != "" {
		conversationText += fmt.Sprintf("\n\n**Important**: Focus the summary on: %s", focus)
	}
	response, err := c.Complete(ctx, system.CompactPrompt(), []core.Message{core.UserMessage(conversationText, nil)}, nil, core.CompactMaxTokens, admission)
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
	if opened == nil || !result.Completion.OwnedBy(opened) {
		return core.Message{}, core.Compacted{}, fmt.Errorf("compaction requires its original session completion")
	}
	summary := core.UserMessage(core.FormatCompactSummary(result.Summary), nil)
	summary.ID = core.NewMessageID()
	info := core.Compacted{Summary: result.Summary, OriginalCount: result.OriginalCount, SummaryMessageID: summary.ID, Trigger: "manual"}
	return summary, info, result.Completion.Compaction(result.Summary).Finish(context.Background(), summary)
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
