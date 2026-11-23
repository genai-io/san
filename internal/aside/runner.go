// Package aside runs one-shot model calls beside the active conversation.
// It never executes tools or applies results to the conversation.
package aside

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
	defer func() { err = FinishChange(ctx, recording, notice, err) }()
	resp, err := Complete(ctx, provider, opts, recording)
	text, err := proposalText(ctx, resp, err)
	if err != nil {
		return "", err
	}
	return apply(ctx, text)
}

// FinishChange records the raw attempt and, only for a successful change, its notice.
func FinishChange(ctx context.Context, recording *session.Completion, notice string, err error) error {
	var events []session.Event
	if err == nil && notice != "" {
		events = append(events, session.Event{Payload: session.AnnotationAddPayload{Text: notice}})
	}
	if finishErr := recording.Finish(ctx, events...); finishErr != nil {
		err = errors.Join(err, &llm.RecordingError{Err: finishErr})
	}
	return err
}

// CompletePrefixedProposal keeps the admitted system, tool schemas and
// conversation prefix and expects a standalone proposal; tool calls are rejected.
func CompletePrefixedProposal(ctx context.Context, provider llm.Provider, model string, request *ai.Request, recording *session.Completion) (string, error) {
	proposal := *request
	// Changing tool_choice invalidates a provider's cached messages, so keep the
	// main request's auto choice and only override one that forces a tool.
	if proposal.ToolChoice != ai.ToolChoiceAuto {
		proposal.ToolChoice = ai.ToolChoiceNone
	}
	proposal.StopSequences = nil
	proposal.Schema = nil
	if proposal.MaxTokens == 0 || proposal.MaxTokens > 4096 {
		proposal.MaxTokens = 4096
	}
	resp, err := llm.CompleteRequest(ctx, provider, model, &proposal, recording.Admission())
	return proposalText(ctx, resp, err)
}

func proposalText(ctx context.Context, resp llm.CompletionResponse, err error) (string, error) {
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
	return resp.Content.Text(), nil
}

// Decide runs a decision adapter that accepts admission and needs no deferred
// result event. It records the attempt even when the adapter rejects the reply.
// A recording failure is joined as *llm.RecordingError so callers fail closed.
func Decide[T any](ctx context.Context, recording *session.Completion, run func(context.Context, llm.CompletionAdmission) (T, error)) (T, error) {
	result, err := run(ctx, recording.Admission())
	if closeErr := recording.Close(); closeErr != nil {
		err = errors.Join(err, &llm.RecordingError{Err: closeErr})
	}
	return result, err
}
