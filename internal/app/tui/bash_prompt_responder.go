package tui

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"

	"github.com/genai-io/san/internal/app/tui/chat"
	"github.com/genai-io/san/internal/aside"
	"github.com/genai-io/san/internal/autopilot/judge"
	"github.com/genai-io/san/internal/infra/log"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
)

type bashPromptResponder struct {
	model     *model
	bridge    *chat.AgentToUI
	recording *session.Session
	sessionID string
}

// RequestAnswer reads live steering only within the originating session.
func (r bashPromptResponder) RequestAnswer(ctx context.Context, command, prompt string) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rt := r.model.autopilot.Load()
	if rt == nil || rt.sessionID != r.sessionID || ctx.Err() != nil {
		return "", false
	}
	scope := r.recording.Completion(session.PurposeAutopilot)
	reply, err := aside.Decide(ctx, scope, func(ctx context.Context, admission llm.CompletionAdmission) (judge.BashPromptReply, error) {
		return rt.judge.BashPrompt(ctx, rt.cfg.ActiveMission(), command, prompt, admission)
	})
	var recordErr *llm.RecordingError
	if errors.As(err, &recordErr) {
		log.Logger().Error("record Bash prompt decision", zap.Error(recordErr))
		return "", false
	}
	log.Logger().Debug("judge prompt answer",
		zap.Bool("answer", err == nil && reply.Answer),
		zap.String("prompt", prompt),
		zap.Error(err))
	if err != nil || ctx.Err() != nil || !reply.Answer {
		return "", false
	}
	return reply.Input, true
}

func (r bashPromptResponder) RequestSecret(ctx context.Context, prompt string) (string, bool) {
	secret, ok, err := r.bridge.RequestSecret(ctx, prompt)
	if err != nil {
		log.Logger().Debug("secret prompt failed", zap.Error(err))
		return "", false
	}
	return secret, ok
}
