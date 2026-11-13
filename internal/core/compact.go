package core

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"

	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

// Shortening a conversation that has outgrown its window.
//
// Neither the exchange's nor the mailbox's: the loop asks at a step boundary
// and again when a provider calls the prompt too long, and the person asks
// through the inbox with /compact. All three collapse to the same one message,
// announced the same way.

// Compaction is a deferred summary. Finish commits the exact native message
// before publication and resolves the accepted scope on success. Close accounts
// for discarded results (or unrecorded results without Finish); it is not called
// after successful Finish. Nil callbacks allow unrecorded use. Producers must
// make Close idempotent and must not commit when Finish returns an error.
type Compaction struct {
	Summary string
	Finish  func(context.Context, Message) error
	Close   func() error
}

func (c Compaction) close() error {
	if c.Close != nil {
		return c.Close()
	}
	return nil
}

// compactionError prevents persistence failures wrapping cancellation from being
// mistaken for a turn-only interrupt.
type compactionError struct{ err error }

func (e *compactionError) Error() string { return e.err.Error() }
func (e *compactionError) Unwrap() error { return e.err }

// MinMessagesToCompact is the shortest conversation worth shortening: an
// opening turn and its reply. Summarising fewer costs a model call to make the
// prompt no shorter.
const MinMessagesToCompact = 3

// TODO(sdk-go): compaction is generic; move it to sdk-go as a package over PreStep/OnInferError, keeping only the prompt and reminders here.
// preStep shortens the conversation once the next prompt would leave the reply
// no room in the window.
func (a *agent) preStep(ctx context.Context, c sdkagent.PreStepContext) ([]Message, error) {
	if a.compactFunc == nil || len(c.Messages) < MinMessagesToCompact {
		return nil, nil
	}
	if a.contextBudget == nil {
		return nil, nil
	}
	budget := a.contextBudget()
	if budget <= 0 || !NeedsCompaction(a.measured.prompt(c.Messages, c.Tokens), budget) {
		return nil, nil
	}
	return a.shorten(ctx, c.Messages, "auto")
}

// onInferError answers the one failure the loop cannot: a prompt the provider
// called too long. Replaying it unchanged fails the same way, so the
// conversation is shortened and the step taken again. Two goes — a summary
// that is still too long is a summarizer problem.
func (a *agent) onInferError(ctx context.Context, c sdkagent.InferErrorContext) (*sdkagent.Recovery, error) {
	if a.compactFunc == nil || !ai.IsContextExceeded(c.Err) || c.Attempt > 2 {
		return nil, nil
	}
	if len(c.Messages) < MinMessagesToCompact {
		return nil, nil
	}
	shorter, err := a.shorten(ctx, c.Messages, "auto")
	if err != nil {
		return nil, err
	}
	if shorter == nil {
		// Nothing left to shorten: give up as the loop would, on the model's
		// own failure rather than on anything invented here.
		return nil, nil
	}
	return &sdkagent.Recovery{Messages: shorter}, nil
}

// shorten is the compaction both hooks share: announce the wait, call the
// application's summarizer, and hand back the conversation that replaces this
// one. Nil means it could not, which both callers read as "leave it alone".
func (a *agent) shorten(ctx context.Context, msgs []Message, trigger string) ([]Message, error) {
	// Shortening is a model call of its own and takes as long as one. The hook
	// says so, because only the code deciding to shorten knows it is about to.
	sdkagent.Compacting(ctx)
	a.measured.reset()

	c, err := a.compactFunc(ctx, msgs)
	if err != nil {
		closeErr := c.close()
		var admission *ai.AdmissionError
		if closeErr != nil {
			return nil, &compactionError{errors.Join(err, closeErr)}
		}
		if errors.As(err, &admission) {
			return nil, &compactionError{err}
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, nil // ordinary provider failures retain the original context
	}
	return a.acceptCompaction(ctx, c, len(msgs), trigger)
}

func (a *agent) applyCompaction(ctx context.Context, c Compaction, originalCount int, trigger string) error {
	msgs, err := a.acceptCompaction(ctx, c, originalCount, trigger)
	if err == nil && msgs != nil {
		a.SetMessages(msgs)
	}
	return err
}

// acceptCompaction resolves the scope before publishing either successful event.
func (a *agent) acceptCompaction(ctx context.Context, c Compaction, originalCount int, trigger string) ([]Message, error) {
	var msg Message
	committed := false
	err := ctx.Err()
	if err == nil && strings.TrimSpace(c.Summary) != "" {
		msg = UserMessage(FormatCompactSummary(c.Summary), nil)
		msg.ID = NewMessageID()
		if c.Finish != nil {
			if finishErr := c.Finish(ctx, msg); finishErr != nil {
				err = &compactionError{finishErr}
			} else {
				committed = true
			}
		}
	}
	// Successful Finish is terminal: a redundant cleanup failure must never
	// roll back acceptance after the durable boundary has been committed.
	if !committed {
		if closeErr := c.close(); closeErr != nil {
			err = &compactionError{errors.Join(err, closeErr)}
		}
	}
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(c.Summary) == "" {
		return nil, nil
	}
	// A successful Finish crosses the acceptance boundary. Cancellation after
	// that point must not leave the committed projection ahead of native history
	// or suppress its boundary events. Canceled delivery uses the existing
	// bounded critical-event path rather than an uncancellable mailbox send.
	publish := func(event Event) {
		if ctx.Err() != nil {
			a.emitFinal(event)
		} else {
			a.emit(ctx, event)
		}
	}
	publish(sdkagent.MessageAdded{Message: msg})
	publish(Compacted{
		Summary:          c.Summary,
		OriginalCount:    originalCount,
		SummaryMessageID: msg.ID,
		Trigger:          trigger,
	})
	return []Message{msg}, nil
}

// promptMeasure anchors the prompt size on the provider's own count. The SDK's
// estimate deliberately runs 12-45% high, which would compact early; the last
// reply's usage is exact, so only what was added since is estimated.
type promptMeasure struct {
	mu sync.Mutex
	// sent is how many messages the call in flight carries. messages and
	// tokens describe the last answered call: the conversation it left behind
	// (its prompt plus the reply) and what the provider counted for them.
	sent, messages, tokens int
}

func (m *promptMeasure) sending(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = n
}

func (m *promptMeasure) answered(u ai.Usage) {
	if u.TotalInput() == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages, m.tokens = m.sent+1, u.Total()
}

// reset forgets the anchor once the conversation is replaced.
func (m *promptMeasure) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages, m.tokens = 0, 0
}

// prompt sizes the next prompt: the anchor plus an estimate of what followed
// it, or the SDK's whole-prompt estimate when there is no anchor.
func (m *promptMeasure) prompt(msgs []Message, estimate int) int {
	m.mu.Lock()
	anchor, tokens := m.messages, m.tokens
	m.mu.Unlock()
	if anchor == 0 || anchor > len(msgs) {
		return estimate
	}
	return tokens + (&ai.Request{Messages: msgs[anchor:]}).EstimateTokens()
}

// CompactMaxTokens is the max output tokens for compaction LLM calls.
const CompactMaxTokens = 4096

// CompactSummaryPrefix marks a user message as the post-compaction summary, so
// the UI draws a notice rather than a user turn. The model and the store keep
// the full text.
const CompactSummaryPrefix = "Previous context:\n"

// FormatCompactSummary marks a summary for injection as a user message.
func FormatCompactSummary(summary string) string {
	return CompactSummaryPrefix + summary
}

// IsCompactSummary reports whether content came from FormatCompactSummary.
func IsCompactSummary(content string) bool {
	return strings.HasPrefix(content, CompactSummaryPrefix)
}

// WithRecentRequests appends the person's latest messages, preserving their
// text verbatim plus markers for attached images, newest kept first, to a compaction summary: what was asked is the one thing a
// paraphrase must not bend. They take at most ~10k tokens, and a tenth of a
// known budget, so a small window still comes out of compaction with room.
func WithRecentRequests(summary string, msgs []Message, budget int) string {
	// Newest first. An earlier summary contributes the messages it kept, not
	// itself, so the request survives compaction after compaction.
	var candidates []string
	for _, msg := range slices.Backward(msgs) {
		if msg.Role != ai.RoleUser || len(msg.ToolResults()) > 0 {
			continue
		}
		text := strings.TrimSpace(compactionUserText(msg))
		if !IsCompactSummary(text) {
			candidates = append(candidates, text)
			continue
		}
		if _, carried, ok := strings.Cut(text, recentRequestsHeader); ok {
			// The section ends where the attached reminders begin. Cut there
			// rather than trusting the strip: a restored file that quotes the
			// tag defeats it.
			carried, _, _ = strings.Cut(carried, "\n\n"+systemReminderOpen)
			earlier := strings.Split(carried, recentRequestsSep)
			slices.Reverse(earlier)
			candidates = append(candidates, earlier...)
		}
	}

	var kept []string
	room := 40_000 // bytes, ~10k tokens
	if budget > 0 {
		room = min(room, budget*4/10)
	}
	for _, text := range candidates {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		if len(text) > room {
			if len(kept) > 0 {
				break
			}
			text = clip(text, room)
		}
		kept = append(kept, text)
		room -= len(text)
	}
	if len(kept) == 0 {
		return summary
	}
	slices.Reverse(kept)
	return summary + recentRequestsHeader + strings.Join(kept, recentRequestsSep)
}

const (
	recentRequestsHeader = "\n\n## Recent User Messages (verbatim)\n\n"
	recentRequestsSep    = "\n\n---\n\n"
)

// --- context keys ---
