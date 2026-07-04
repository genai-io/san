package tool

import "context"

// BashPromptResponder supplies the input for an interactive prompt a tool raises
// while it runs, returning the line to feed the prompt and ok=false to decline.
// RequestAnswer handles an ordinary prompt whose reply may be decided
// autonomously; RequestSecret handles a credential prompt whose value must come
// from a person. A secret must be written directly to the child process and must
// not be logged, recorded, or sent to an LLM.
type BashPromptResponder interface {
	RequestAnswer(ctx context.Context, command, prompt string) (input string, ok bool)
	RequestSecret(ctx context.Context, prompt string) (secret string, ok bool)
}

// BashPromptResponderProvider returns the responder for a specific tool execution.
// Returning nil leaves the tool on its non-interactive path.
type BashPromptResponderProvider func(context.Context) BashPromptResponder

type promptResponderProviderKey struct{}

// ContextWithBashPromptResponderProvider stores a per-execution prompt responder provider
// in ctx for tools that know how to use interactive prompts.
func ContextWithBashPromptResponderProvider(ctx context.Context, fn BashPromptResponderProvider) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, promptResponderProviderKey{}, fn)
}

// BashPromptResponderFromContext resolves the current prompt responder, if any.
func BashPromptResponderFromContext(ctx context.Context) BashPromptResponder {
	if ctx == nil {
		return nil
	}
	fn, _ := ctx.Value(promptResponderProviderKey{}).(BashPromptResponderProvider)
	if fn == nil {
		return nil
	}
	return fn(ctx)
}
