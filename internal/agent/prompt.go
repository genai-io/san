package agent

import (
	"context"
	"errors"

	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/reminder"
)

// CheckPrompt applies UserPromptSubmit policy and stages context for later delivery.
// Call only at prompt admission, not when delivering already-admitted or background input.
// The caller supplies the hook budget; engine errors fail open, not as prompt cancellation.
func CheckPrompt(ctx context.Context, prompt string, hooks *hook.Engine, reminders *reminder.Reminder) (blocked bool, reason string) {
	outcome := hooks.Execute(ctx, hook.UserPromptSubmit, hook.HookInput{Prompt: prompt})
	var recording *hook.RecordingError
	if errors.As(outcome.Error, &recording) {
		return true, recording.Error()
	}
	// Context remains pending even on a block, and may reach a later input.
	// Admission must not drain reminders or prepare conversation context.
	if outcome.AdditionalContext != "" {
		reminders.Enqueue(outcome.AdditionalContext)
	}
	return outcome.ShouldBlock, outcome.BlockReason
}
