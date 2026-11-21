// Package sidecard runs one-shot model calls beside the active conversation.
// It never executes tools or applies results to the conversation.
package sidecard

import (
	"context"
	"errors"

	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/ai"
)

// Complete runs a standalone call without advertised tools, using the supplied
// recording for admission. The caller resolves the recording after deciding
// whether to apply the result.
func Complete(ctx context.Context, provider llm.Provider, opts llm.CompletionOptions, recording *session.Completion) (llm.CompletionResponse, error) {
	opts.Tools = nil
	opts.Admission = recording.Admission()
	return llm.Complete(ctx, provider, opts)
}

// ApplyChange calls the model without tools and hands a complete proposal to
// the caller for validation and application. Only a committed change produces
// a source-linked notice; a no-op or rejection still records the raw response.
func ApplyChange(ctx context.Context, provider llm.Provider, opts llm.CompletionOptions, recording *session.Completion, apply func(context.Context, string) (string, error)) (notice string, err error) {
	defer func() {
		var events []session.Event
		if err == nil && notice != "" {
			events = append(events, session.Event{Payload: session.AnnotationAddPayload{Text: notice}})
		}
		if finishErr := recording.Finish(ctx, events...); finishErr != nil {
			err = errors.Join(err, &llm.RecordingError{Err: finishErr})
		}
	}()
	resp, err := Complete(ctx, provider, opts, recording)
	if err != nil {
		return "", err
	}
	if resp.Err != nil {
		return "", resp.Err
	}
	if resp.StopReason != ai.StopEndTurn || len(resp.Content.ToolCalls()) != 0 {
		return "", errors.New("incomplete or tool-bearing proposal")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return apply(ctx, resp.Content.Text())
}

// Decide runs a decision adapter that accepts admission and needs no deferred
// result event. It records the attempt even when the adapter rejects the reply.
// The separate recording error lets callers fail closed on audit failure.
func Decide[T any](ctx context.Context, recording *session.Completion, run func(context.Context, llm.CompletionAdmission) (T, error)) (result T, err, recordErr error) {
	result, err = run(ctx, recording.Admission())
	return result, err, recording.Close()
}
