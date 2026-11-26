package hook

import (
	"context"
	"errors"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/ai"
)

// Bind captures the native owner; child engines must not reuse a
// parent's completion recorder or model-completion closure.
func Bind(engine *hook.Engine, provider llm.Provider, owner *session.Session, model string) {
	if engine == nil {
		return
	}
	engine.SetAuditCallback(nil)
	engine.SetLLMCompleter(nil, model)
	engine.SetRecordedLLMCompleter(Completer(provider, owner), model)
	engine.SetCompletionRecorder(session.NewRecorder(owner, "").RecordHook)
}

// Completer runs hook model calls as recorded completions of owner.
func Completer(p llm.Provider, owner *session.Session) hook.RecordedLLMCompleter {
	if p == nil {
		return nil
	}
	return func(ctx context.Context, systemPrompt, userMessage, model string) (hook.LLMCompletion, error) {
		scope := owner.Completion(session.PurposeHook)
		c := llm.NewClient(p, model, 0)
		resp, err := c.Complete(ctx, systemPrompt, []core.Message{core.UserMessage(userMessage, nil)}, nil, 4096, scope.Admission())
		result := hook.LLMCompletion{Text: resp.Content.Text(), Finish: scope.FinishHook}
		var admission *ai.AdmissionError
		var recording *llm.RecordingError
		if errors.As(err, &admission) || errors.As(err, &recording) {
			err = &hook.RecordingError{Err: err}
		}
		return result, err
	}
}
