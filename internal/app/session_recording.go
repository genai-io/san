package app

import (
	"errors"

	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
)

// bindSessionHooks captures the native owner; child engines must not reuse a
// parent's completion recorder or model-completion closure.
func bindSessionHooks(engine *hook.Engine, provider llm.Provider, owner *session.Session, model string) {
	if engine == nil {
		return
	}
	engine.SetAuditCallback(nil)
	engine.SetLLMCompleter(nil, model)
	engine.SetRecordedLLMCompleter(buildRecordedHookCompleter(provider, owner), model)
	engine.SetCompletionRecorder(session.NewRecorder(owner, "").RecordHook)
}

// closeSessionRecording runs after runtime and task shutdown, while hooks can
// still commit their outcomes to the original writer.
func (m *model) closeSessionRecording() error {
	if m.services.Hook != nil {
		m.services.Hook.Shutdown()
	}
	var err error
	if m.services.Session != nil {
		err = m.services.Session.Close()
	}
	if m.services.SessionStore != nil {
		err = errors.Join(err, m.services.SessionStore.Close())
	}
	return err
}
