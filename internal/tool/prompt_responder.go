package tool

import "context"

// PromptResponder decides how to answer an interactive prompt a tool raises
// while it runs. A secret returned by RequestSecret must be written directly to
// the child process and must not be logged, recorded, or sent to an LLM.
type PromptResponder interface {
	AnswerPrompt(ctx context.Context, command, prompt string) (input string, ok bool)
	RequestSecret(ctx context.Context, prompt string) (secret string, ok bool)
}

// PromptResponderProvider returns the responder for a specific tool execution.
// Returning nil leaves the tool on its non-interactive path.
type PromptResponderProvider func(context.Context) PromptResponder

type promptResponderProviderKey struct{}

// ContextWithPromptResponderProvider stores a per-execution prompt responder provider
// in ctx for tools that know how to use interactive prompts.
func ContextWithPromptResponderProvider(ctx context.Context, fn PromptResponderProvider) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, promptResponderProviderKey{}, fn)
}

// PromptResponderFromContext resolves the current prompt responder, if any.
func PromptResponderFromContext(ctx context.Context) PromptResponder {
	if ctx == nil {
		return nil
	}
	fn, _ := ctx.Value(promptResponderProviderKey{}).(PromptResponderProvider)
	if fn == nil {
		return nil
	}
	return fn(ctx)
}
