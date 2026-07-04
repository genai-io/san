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

func (r bashPromptResponder) AnswerPrompt(ctx context.Context, command, prompt string) (string, bool) {
	// The provider closure only builds this responder when auto-review is on, so
	// no mode re-check here; guard just the dependencies.
	if r.model == nil || r.reviewer == nil {
		return "", false
	}
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
	if r.model == nil || r.model.conv.ProgressHub == nil {
		return "", false
	}
	secret, ok, err := r.model.conv.ProgressHub.RequestSecret(ctx, prompt)
	if err != nil {
		log.Logger().Debug("secret prompt failed", zap.Error(err))
		return "", false
	}
	return secret, ok
}
