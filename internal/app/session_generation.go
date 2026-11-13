package app

import (
	"context"
	"fmt"

	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
)

// sessionMaxOutput treats a saved session cap as authoritative, not a default
// to be silently replaced by changes to the global model catalogue.
func (m *model) sessionMaxOutput() int {
	if owner := m.services.Session; owner != nil {
		snapshot := owner.Snapshot()
		if g := snapshot.Config().GenerationOptions; g != nil && g.MaxTokens != nil {
			return *g.MaxTokens
		}
	}
	var store *llm.Store
	if m.services.LLM != nil {
		store = m.services.LLM.Store()
	}
	return kit.GetMaxOutput(store, m.env.CurrentModel, setting.DefaultMaxTokens)
}

// applyContextLimitUpdate commits the explicit session edit before retiring the
// old runtime. Commands delayed across a session/model switch cannot edit it.
func (m *model) applyContextLimitUpdate(msg input.ContextLimitUpdatedMsg) error {
	owner := m.services.Session
	if owner == nil || owner != msg.Session || m.env.CurrentModel == nil || m.env.GetModelID() != msg.ModelID || string(m.env.CurrentModel.Provider) != msg.Provider {
		return nil
	}
	if msg.MaxTokens <= 0 {
		return fmt.Errorf("invalid session max output %d", msg.MaxTokens)
	}
	snapshot := owner.Snapshot()
	generation := session.GenerationOptions{Version: 1}
	if saved := snapshot.Config().GenerationOptions; saved != nil {
		generation = *saved
	}
	generation.MaxTokens = &msg.MaxTokens
	if err := session.NewRecorder(owner, "").RecordState(context.Background(), session.SessionUpdatePayload{Config: &session.ConfigPatch{GenerationOptions: session.SetField(generation)}}); err != nil {
		return err
	}
	// The next turn rebuilds from this same native owner and its retained history.
	if m.services.Agent != nil && m.services.Agent.Active() {
		return m.StopAgentRuntime()
	}
	return nil
}
