package session

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/sdk-go/pkg/ai"
)

// Completion owns one utility request, including its retries, until its domain
// owner accepts or discards the final result. It never redirects to a new session.
type Completion struct {
	mu       sync.Mutex
	session  *Session
	purpose  string
	append   []ai.Message
	current  *Inference
	response *ai.Response
	callErr  error
	returned bool
	discard  bool
	closed   bool
	err      error
}

// Completion returns nil for an unconfigured typed session, preserving the
// existing completion path during the unwired migration.
func (s *Session) Completion(purpose string, extra ...ai.Message) *Completion {
	if s == nil {
		return nil
	}
	appended := slices.Clone(extra)
	for i := range appended {
		appended[i].Content = appended[i].Content.Clone()
	}
	return &Completion{session: s, purpose: purpose, append: appended}
}

// Admission is supplied per call, never installed as mutable provider state.
func (c *Completion) Admission() llm.CompletionAdmission {
	if c == nil {
		return nil
	}
	return c.admit
}

func (c *Completion) admit(ctx context.Context, provider string, model ai.Model, request *ai.Request) (llm.CompletionFinish, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return nil, c.err
	}
	if c.closed {
		return nil, fmt.Errorf("completion is closed")
	}
	if c.current != nil {
		if !c.returned || c.callErr == nil {
			return nil, fmt.Errorf("previous completion has not been resolved")
		}
		// A retry closes the previous failed attempt before its next dispatch.
		// The final attempt waits for the owner so its result can share a batch.
		if err := c.current.Finish(context.WithoutCancel(ctx), c.response, c.callErr); err != nil {
			c.err = err
			return nil, err
		}
		c.current = nil
	}
	inference, err := c.session.StartInference(ctx, InferenceOptions{Purpose: c.purpose,
		Provider: provider, Model: model, Request: request, Append: c.append})
	if err != nil {
		c.err = err
		return nil, err
	}
	c.current, c.response, c.callErr, c.returned = inference, nil, nil, false
	return func(persist context.Context, response *ai.Response, callErr error) error {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.closed || c.current != inference || c.returned {
			return fmt.Errorf("completion attempt already returned")
		}
		if response != nil {
			copy := *response
			// Only accounting and outcome are needed here; accepted content is
			// supplied by the owner as its typed result event.
			copy.Content = nil
			c.response = &copy
			if callErr == nil {
				callErr = response.Err
			}
		}
		c.callErr, c.returned = callErr, true
		if c.discard {
			c.err = inference.Finish(context.WithoutCancel(persist), c.response, c.callErr)
			c.closed = c.err == nil
			return c.err
		}
		return nil
	}, nil
}

// Finish commits the last attempt and the owner's accepted result in one batch.
// Error/obsolete results may finish without publishing a conversation event.
func (c *Completion) Finish(ctx context.Context, results ...Event) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return c.err
	}
	if c.closed {
		if len(results) > 0 {
			return fmt.Errorf("completion result already recorded")
		}
		return nil
	}
	if c.current == nil {
		if len(results) > 0 {
			return fmt.Errorf("completion has no admitted attempt")
		}
		c.closed = true
		return nil
	}
	if !c.returned {
		return fmt.Errorf("completion is still running")
	}
	if err := c.current.Finish(context.WithoutCancel(ctx), c.response, c.callErr, results...); err != nil {
		c.err = err
		return err
	}
	c.closed = true
	return nil
}

// OwnedBy compares session handles, not just their IDs.
func (c *Completion) OwnedBy(owner *Session) bool {
	return c != nil && c.session == owner
}

// FinishMessage commits the exact native input accepted by the runtime.
func (c *Completion) FinishMessage(ctx context.Context, message core.Message) error {
	return c.Finish(ctx, Event{Payload: MessageAppendPayload{Messages: []ai.Message{message}}})
}

// Compaction defers the context boundary until core supplies the accepted native
// summary, including its final instructions, formatting and stable message ID.
func (c *Completion) Compaction(summary string) core.Compaction {
	result := core.Compaction{Summary: summary}
	if c != nil {
		result.Finish = func(ctx context.Context, message core.Message) error {
			return c.Finish(ctx, Event{Payload: ContextCompactPayload{Summary: message}})
		}
		result.Close = c.Close
	}
	return result
}

// FinishPermission joins the final gate verdict with its judge inference.
func (c *Completion) FinishPermission(ctx context.Context, resolved perm.Resolution) error {
	return c.Finish(ctx, Event{Payload: permissionPayload(resolved)})
}

// FinishHook joins the interpreted hook outcome with its model-call finish.
func (c *Completion) FinishHook(ctx context.Context, audit hook.HookFiredAudit) error {
	if c == nil {
		return nil
	}
	if audit.Outcome == "async" {
		return fmt.Errorf("async launch is not a completed hook")
	}
	duration := audit.Duration.Milliseconds()
	reason := boundedReason(audit.Reason)
	if reason == "" && (audit.Outcome == HookBlocked || audit.Outcome == HookError) {
		reason = "hook " + audit.Outcome
	}
	event := Event{Payload: HookFinishPayload{Hook: audit.Source, Trigger: audit.Event,
		Matcher: audit.Matcher, Outcome: audit.Outcome, DurationMS: &duration, Reason: reason}}
	c.mu.Lock()
	if c.current == nil && !c.closed && c.err == nil {
		// Reaching/validating a provider can fail before admission. The hook
		// still completed, but no model call may be invented for it.
		err := c.session.Append(context.WithoutCancel(ctx), event)
		c.err, c.closed = err, err == nil
		c.mu.Unlock()
		return err
	}
	c.mu.Unlock()
	return c.Finish(ctx, event)
}

// Close discards the result. If a call is still running, its finisher will
// account for it without publishing output, even if the UI has already exited.
func (c *Completion) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	if c.current != nil && !c.returned && c.err == nil {
		c.discard = true
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()
	return c.Finish(context.Background())
}
