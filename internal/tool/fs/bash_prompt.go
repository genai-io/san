package fs

import "context"

// PromptResponder decides how to answer an interactive prompt a bash command
// raises while it runs. It is consulted only in auto-review mode.
//
// The two methods keep a hard security boundary: a secret (password/passphrase)
// is handled by RequestSecret and its value must go straight to the process —
// it is never passed to AnswerPrompt, a model, a log, or the transcript.
type PromptResponder interface {
	// AnswerPrompt handles a non-secret prompt. It returns the input to send
	// (without trailing newline) and ok=true to answer, or ok=false to skip so
	// the command fails fast instead of proceeding.
	AnswerPrompt(ctx context.Context, command, prompt string) (input string, ok bool)

	// RequestSecret handles a password/passphrase prompt. The returned value is
	// written straight to the process and MUST NOT reach any model, log, or
	// transcript. ok=false skips.
	RequestSecret(ctx context.Context, prompt string) (secret string, ok bool)
}
