package app

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/reviewer"
)

type bashPromptResponder struct {
	model    *model
	reviewer *reviewer.AutoReview
}

// RequestAnswer delegates an ordinary prompt to the auto-review LLM, which
// decides the reply. It never sees a secret prompt — that goes to RequestSecret.
// The provider closure only builds this responder when auto-review is on, so
// there is no mode re-check here.
func (r bashPromptResponder) RequestAnswer(ctx context.Context, command, prompt string) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	reply, err := r.reviewer.BashPrompt(ctx, command, prompt)
	log.Logger().Debug("auto-review prompt answer",
		zap.Bool("answer", err == nil && reply.Answer),
		zap.String("prompt", prompt),
		zap.Error(err))
	if err != nil || !reply.Answer {
		return "", false
	}
	return reply.Input, true
}

func (r bashPromptResponder) RequestSecret(ctx context.Context, prompt string) (string, bool) {
	secret, ok, err := r.model.conv.AgentToUI.RequestSecret(ctx, prompt)
	if err != nil {
		log.Logger().Debug("secret prompt failed", zap.Error(err))
		return "", false
	}
	return secret, ok
}
